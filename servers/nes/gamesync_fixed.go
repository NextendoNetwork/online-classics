package main

// Genesis 3.1.1 readers at main 0x300a7c confirm the names and types
// of gsid, addr, p, mcn, maxu and rs. This does not confirm every value or
// the transport. tid comes from the public reference. rs is random, private
// and stable for the lifetime of this room.
func fixedSessionFields(ticket pendingTicket, secret []byte) []gsField {
	capacity := ticket.request.capacity
	if capacity < 2 || capacity > 16 {
		capacity = 2
	} // Same value as ticketSucceededResponse.
	str := func(s string) []byte { return protoBytes(nil, 7, []byte(s)) }
	return []gsField{
		{"addr", str(labTenant + ".lp1.t.npln.srv.nintendo.net")},
		{"gsid", str(resourceLeaf(ticket.sessionName))},
		{"maxu", protoVarint(nil, 3, capacity)},
		{"mcn", str(resourceLeaf(ticket.config))},
		{"p", protoVarint(nil, 3, 443)},
		{"rs", protoBytes(nil, 8, secret)},
		{"tid", str(labTenant)},
	}
}
