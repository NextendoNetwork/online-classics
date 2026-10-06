package main

import (
	"testing"

	commonpb "npln.nintendo.net/npln-practice/proto/common"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func TestClassicsConfigCompatibilityRejectsUnobservedPrefixes(t *testing.T) {
	for _, tc := range []struct {
		search, creation string
		want             bool
	}{
		{"LCLA6", "LCLA6-4P", true}, {"LCLA6", "LCLA6-2P", true},
		{"LCLA6-4P", "LCLA6-4P", true}, {"LCLA6-2P", "LCLA6-4P", false},
		{"LCLA6", "LCLA6-4P-future", false}, {"LCLA6", "LCLA6-unrelated", false},
		{"LCLA", "LCLA6-4P", false},
	} {
		if got := configsCompatible(tc.search, tc.creation); got != tc.want {
			t.Errorf("%s / %s: %v", tc.search, tc.creation, got)
		}
	}
}

func TestClassicsPropertyAliasesPreferExactValues(t *testing.T) {
	fields := map[string]*commonpb.Value{"applicationVersion": gamesyncStringValue("4.2.0"), "ApplicationVersion": gamesyncStringValue("incompatible"), "consoleName": gamesyncStringValue("Nintendo64"), "matchingkey": gamesyncStringValue("unrelated")}
	if sessionProperty(fields, "ApplicationVersion").GetStringValue() != "incompatible" {
		t.Fatal("alias overrode exact version")
	}
	if sessionProperty(fields, "ConsoleName").GetStringValue() != "Nintendo64" {
		t.Fatal("console alias missing")
	}
	if sessionProperty(fields, "MatchingKey") != nil {
		t.Fatal("arbitrary property aliases accepted")
	}
}

func TestClassicsHostExitClosesRoomButGuestExitDoesNot(t *testing.T) {
	for _, leaving := range []string{"host", "guest"} {
		t.Run(leaving, func(t *testing.T) {
			r := newSessionRegistry()
			gs := &mmpb.GameSession{Name: nplnTenant + "/gameSessions/room", State: mmpb.GameSession_ACTIVE, CanParticipate: true, MaxParticipantCount: 4, CurrentParticipantCount: 2, Properties: &commonpb.MapValue{Fields: map[string]*commonpb.Value{"_BaseConfigName": gamesyncStringValue("LCLA6-4P")}}, UserSessions: []*mmpb.UserSession{
				{Name: "host", User: nplnTenant + "/users/u-host", State: mmpb.UserSession_ACTIVE},
				{Name: "guest", User: nplnTenant + "/users/u-guest", State: mmpb.UserSession_ACTIVE},
			}}
			r.sessions["room"] = gs
			r.depart(gs.Name, leaving)
			if gs.CurrentParticipantCount != 1 {
				t.Fatal("incorrect occupancy after exit")
			}
			if leaving == "host" {
				if gs.State != mmpb.GameSession_TERMINATED || gs.CanParticipate || roomVisible(gs, "u-guest", nil) {
					t.Fatal("hostless room remained discoverable")
				}
				if session, _ := r.member(gs.Name, "guest", "u-guest"); session != nil {
					t.Fatal("terminated room accepted membership")
				}
			} else if gs.State != mmpb.GameSession_ACTIVE || !gs.CanParticipate {
				t.Fatal("guest exit closed host room")
			}
		})
	}
}
