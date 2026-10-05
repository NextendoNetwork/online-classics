package main

// Opt-in experiment: __stg/All shape from the public Nextendo/Splatoon 3
// reference. The contract and cp/ebf/bfmin/bfmax flags are NOT yet confirmed
// for Genesis. Do not invent __stu state or its connection sequences.
// Here rs is a settings map, not the secret in __gs/f.
func experimentalStageFields(ticket pendingTicket) []gsField {
	capacity := ticket.request.capacity
	if capacity < 2 || capacity > 16 {
		capacity = 2
	}
	str := func(s string) []byte { return protoBytes(nil, 7, []byte(s)) }
	boolean := func(b bool) []byte {
		var n uint64
		if b {
			n = 1
		}
		return protoVarint(nil, 2, n)
	}
	settings := []gsField{
		{"cp", boolean(false)},
		{"ip", boolean(ticket.request.isPublic)},
		{"pw", str(ticket.request.password)},
		{"ebf", boolean(false)},
		{"bfmin", gsIntValue(1)},
		{"bfmax", protoVarint(nil, 3, capacity)},
		{"prp", protoBytes(nil, 10, ticket.request.properties)},
	}
	var encoded []byte
	for _, f := range settings {
		entry := protoBytes(nil, 1, []byte(f.key))
		entry = protoBytes(entry, 2, f.value)
		encoded = protoBytes(encoded, 1, entry)
	}
	return []gsField{
		{"gsid", str(resourceLeaf(ticket.sessionName))},
		{"addr", str(labTenant + ".lp1.t.npln.srv.nintendo.net")},
		{"p", gsIntValue(443)},
		{"mcn", str(resourceLeaf(ticket.config))},
		{"maxu", protoVarint(nil, 3, capacity)},
		{"rs", protoBytes(nil, 10, encoded)},
	}
}
