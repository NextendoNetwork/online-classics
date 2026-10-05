package main

import (
	"crypto/sha256"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const gamesyncWriteDocumentsPath = "/nn.npln.gamesync.v1.Gamesync/WriteDocuments"

// WriteDocuments against the actual per-room store. Returns gRPC 0 only when
// ALL operations have been validated and committed. An operation with unknown
// semantics rejects the entire batch with an explicit status and leaves no
// changes. Logs retain structure only, never values.
func (s *ticketStore) inspectGamesyncWrites(logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		if tenant := r.Header.Get("npln-tenant-id"); tenant != "" && tenant != labTenant {
			grpcStatus(w, "3", "Incorrect tenant", nil)
			return
		}
		parts := strings.Fields(r.Header.Get("authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			grpcStatus(w, "16", "Missing local Gamesync token", nil)
			return
		}
		s.mu.Lock()
		issued, exists := s.issuedGamesync[sha256.Sum256([]byte(parts[1]))]
		s.mu.Unlock()
		if !exists || !time.Now().Before(issued.expires) {
			grpcStatus(w, "16", "Invalid local Gamesync token", nil)
			return
		}
		payload, err := grpcRequestPayload(r)
		if err != nil {
			grpcStatus(w, "3", "Invalid frame", nil)
			return
		}
		writes, deferments, err := logGamesyncWriteStructure(payload, issued, logger)
		if err != nil {
			grpcStatus(w, "3", "Invalid write", nil)
			return
		}
		ops, defs, perr := gsParseWriteRequest(payload)
		if perr != nil {
			logger.Printf("Gamesync WriteDocuments: writes=%d deferments=%d applied=false rejection=%s reason=%q", writes, deferments, perr.code, perr.message)
			grpcStatus(w, perr.code, perr.message, nil)
			return
		}
		logGamesyncWriteLabels(ops, defs, logger)
		actor := gamesyncActorFor(issued.ticket)
		now := time.Now().UTC()
		room := s.gamesyncRoom(issued.ticket)
		room.logSignalStructure(actor, ops, logger)
		results, changed, aerr := room.applyWrites(actor, ops, defs, now)
		if aerr != nil {
			logger.Printf("Gamesync WriteDocuments: writes=%d deferments=%d applied=false rejection=%s reason=%q", writes, deferments, aerr.code, aerr.message)
			grpcStatus(w, aerr.code, aerr.message, nil)
			return
		}
		var response []byte
		for _, result := range results {
			response = protoBytes(response, 1, result)
		}
		response = protoBytes(response, 2, protoTimestamp(now))
		logger.Printf("Gamesync WriteDocuments: writes=%d deferments=%d applied=true modified_documents=%d", writes, deferments, changed)
		if s.genesisStage {
			room.logParticipantStructure(actor, logger)
		}
		grpcStatus(w, "0", "", response)
	}
}

// Structure of parsed operations without values: only known schema labels
// are printed as strings.
func logGamesyncWriteLabels(ops []gsWrite, defs []gsDeferment, logger *log.Logger) {
	for _, op := range ops {
		var names []string
		for _, f := range op.fields {
			names = append(names, gsLabel(f.key, "{field}"))
		}
		inMask := 0
		for _, f := range op.fields {
			if gsInMask(op.mask, f.key) {
				inMask++
			}
		}
		wildcard := false
		var maskNames []string
		for _, p := range op.mask {
			wildcard = wildcard || p == "*"
			maskNames = append(maskNames, gsLabel(p, "{field}"))
		}
		logger.Printf("Gamesync parsed write: operation=%d path_schema=%q fields=%v mask=%v masked_fields=%d unmasked_fields=%d wildcard_mask=%t",
			op.kind, documentPathShape(op.path), names, maskNames, inMask, len(op.fields)-inMask, wildcard)
		for _, t := range op.transforms {
			field, _ := gsFlatFieldPath(t.path)
			logger.Printf("Gamesync parsed transform: type=%d field=%s delimited_field=%t global=%s", t.kind, gsLabel(field, "{field}"), strings.HasPrefix(t.path, "`"), gsLabel(t.global, "{global}"))
		}
	}
	for _, d := range defs {
		logger.Printf("Gamesync parsed deferment: update=%t name_schema=%q writes=%d", d.update, documentPathShape(d.name), len(d.ops))
	}
}

// Contract observation: counts operations and summarizes paths without retaining
// or printing payloads. Returns the number of writes and deferments.
func logGamesyncWriteStructure(payload []byte, issued issuedMatch, logger *log.Logger) (int, int, error) {
	writes, deferments := 0, 0
	err := visitProto(payload, func(f, wire, _ uint64, v []byte) error {
		if f != 1 && f != 2 {
			return nil
		}
		if wire != 2 {
			return errors.New("Invalid operation")
		}
		if f == 2 {
			deferments++
			return inspectGamesyncDeferment(v, logger)
		}
		writes++
		kind := uint64(0)
		return visitProto(v, func(op, wire, _ uint64, data []byte) error {
			if op < 1 || op > 4 {
				return nil
			}
			if wire != 2 || kind != 0 {
				return errors.New("Invalid oneof")
			}
			kind = op
			var name string
			mask, precondition, fields, transforms := false, false, 0, 0
			err := visitProto(data, func(field, wire, _ uint64, value []byte) error {
				if field == 1 {
					if wire != 2 {
						return errors.New("Invalid reference")
					}
					if op >= 3 {
						name = string(value)
					} else {
						return visitProto(value, func(df, dw, _ uint64, dv []byte) error {
							if df == 1 && dw == 2 {
								name = string(dv)
							}
							if df == 2 && dw == 2 {
								return visitProto(dv, func(mf, mw, _ uint64, _ []byte) error {
									if mf == 1 && mw == 2 {
										fields++
									}
									return nil
								})
							}
							return nil
						})
					}
				}
				if op == 1 && field == 2 {
					mask = true
					if wire != 2 {
						return errors.New("Invalid mask")
					}
					if err := visitProto(value, func(mf, mw, _ uint64, path []byte) error {
						if mf == 1 && mw == 2 {
							logger.Printf("Gamesync mask: field_length=%d segments=%d", len(path), strings.Count(string(path), ".")+1)
						}
						return nil
					}); err != nil {
						return err
					}
				}
				if (op == 1 && field == 3) || (op == 4 && field == 2) {
					precondition = true
				}
				if op == 3 && field == 2 {
					transforms++
					if wire != 2 {
						return errors.New("Invalid transform")
					}
					if err := inspectGamesyncTransform(value, logger); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			logger.Printf("Gamesync WriteDocuments diagnostic: operation=%d path_schema=%q fields=%d mask=%t precondition=%t transforms=%d id_matches_usid=%t id_matches_gsid=%t", op, documentPathShape(name), fields, mask, precondition, transforms, resourceLeaf(name) == resourceLeaf(issued.ticket.userName), resourceLeaf(name) == resourceLeaf(issued.ticket.sessionName))
			return nil
		})
	})
	return writes, deferments, err
}

func inspectGamesyncTransform(payload []byte, logger *log.Logger) error {
	var path string
	var kind, valueType uint64
	err := visitProto(payload, func(f, wire, _ uint64, value []byte) error {
		if f == 1 {
			if wire != 2 {
				return errors.New("Invalid transform field")
			}
			path = string(value)
		}
		if f >= 2 && f <= 8 {
			if kind != 0 {
				return errors.New("Duplicate transform oneof")
			}
			kind = f
			if f >= 3 && f <= 5 {
				if wire != 2 {
					return errors.New("Invalid transform value")
				}
				return visitProto(value, func(vf, _, _ uint64, _ []byte) error {
					if vf >= 1 && vf <= 11 {
						valueType = vf
					}
					return nil
				})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if path == "" || kind == 0 {
		return errors.New("Incomplete transform")
	}
	logger.Printf("Gamesync transform: type=%d value_type=%d field_length=%d segments=%d", kind, valueType, len(path), strings.Count(path, ".")+1)
	return nil
}

func inspectGamesyncDeferment(payload []byte, logger *log.Logger) error {
	var kind uint64
	err := visitProto(payload, func(f, wire, _ uint64, value []byte) error {
		if f != 1 && f != 2 {
			return nil
		}
		if kind != 0 || wire != 2 {
			return errors.New("Invalid deferment")
		}
		kind = f
		var name string
		writes := 0
		err := visitProto(value, func(df, dw, _ uint64, dv []byte) error {
			if df == 1 && dw == 2 {
				if f == 2 {
					name = string(dv)
					return nil
				}
				return visitProto(dv, func(sf, sw, _ uint64, sv []byte) error {
					if sf == 1 && sw == 2 {
						name = string(sv)
					}
					if sf == 2 && sw == 2 {
						writes++
						return visitProto(sv, func(op, ow, _ uint64, data []byte) error {
							if op < 1 || op > 4 || ow != 2 {
								return nil
							}
							var path string
							if op >= 3 {
								path, _ = protoStringField1(data)
							} else {
								_ = visitProto(data, func(uf, uw, _ uint64, doc []byte) error {
									if uf == 1 && uw == 2 {
										path, _ = protoStringField1(doc)
									}
									return nil
								})
							}
							logger.Printf("Gamesync deferment write: operation=%d path_schema=%q", op, documentPathShape(path))
							return nil
						})
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		logger.Printf("Gamesync deferment: type=%d name_schema=%q writes=%d", f, documentPathShape(name), writes)
		return nil
	})
	if err == nil && kind == 0 {
		return errors.New("Empty deferment")
	}
	return err
}
