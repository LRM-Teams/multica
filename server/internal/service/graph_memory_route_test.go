package service

import "testing"

const (
	routeWS   = "11111111-1111-1111-1111-111111111111"
	routeChan = "33333333-3333-3333-3333-333333333333"
	routePA   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	routePB   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

type routeTransitionWant struct {
	next         graphRouteState
	closeCurrent bool
	appendLine   bool
}

func TestResolveChannelRouteTransition(t *testing.T) {
	cases := []struct {
		name  string
		cur   graphRouteState
		bound string
		want  routeTransitionWant
	}{
		{"first use unbound -> permanent standalone channel graph",
			graphRouteState{}, "",
			routeTransitionWant{graphRouteState{found: true, routingMode: "standalone", graphKind: "channel", ownerID: routeChan, generation: 1}, false, true}},
		{"first use bound -> project lineage",
			graphRouteState{}, routePA,
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePA, generation: 1}, false, true}},
		{"standalone stays standalone after later bind",
			graphRouteState{found: true, routingMode: "standalone", graphKind: "channel", ownerID: routeChan, generation: 1}, routePA,
			routeTransitionWant{graphRouteState{found: true, routingMode: "standalone", graphKind: "channel", ownerID: routeChan, generation: 1}, false, false}},
		{"project A -> B closes A and opens B generation",
			graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePA, generation: 3}, routePB,
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePB, generation: 4}, true, true}},
		{"project bound -> unbound opens temporary channel generation",
			graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePA, generation: 3}, "",
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "channel", ownerID: routeChan, generation: 4}, true, true}},
		{"temporary channel generation -> rebind closes it for the new project",
			graphRouteState{found: true, routingMode: "project_lineage", graphKind: "channel", ownerID: routeChan, generation: 4}, routePB,
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePB, generation: 5}, true, true}},
		{"same binding is a no-op",
			graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePA, generation: 3}, routePA,
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "project", ownerID: routePA, generation: 3}, false, false}},
		{"unbound temporary channel generation is a no-op while unbound",
			graphRouteState{found: true, routingMode: "project_lineage", graphKind: "channel", ownerID: routeChan, generation: 4}, "",
			routeTransitionWant{graphRouteState{found: true, routingMode: "project_lineage", graphKind: "channel", ownerID: routeChan, generation: 4}, false, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, closeCurrent, appendLine := resolveChannelRouteTransition(tc.cur, routeChan, tc.bound)
			if next != tc.want.next || closeCurrent != tc.want.closeCurrent || appendLine != tc.want.appendLine {
				t.Fatalf("got (%+v, %v, %v), want (%+v, %v, %v)",
					next, closeCurrent, appendLine, tc.want.next, tc.want.closeCurrent, tc.want.appendLine)
			}
		})
	}
}
