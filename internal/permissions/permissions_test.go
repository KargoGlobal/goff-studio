package permissions

import (
	"testing"
)

func testSet(t *testing.T) *Set {
	t.Helper()
	set, err := New([]Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "payments-team", Teams: []string{"payments"}, Environments: []string{"dev", "staging", "production"}},
		{Group: "marketing", Teams: []string{"growth"}, Environments: []string{"production"}, Actions: []string{"toggle", "rollout"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestDefaultDenyWithNoMatchingRule(t *testing.T) {
	set := testSet(t)

	if set.Allowed(Request{Groups: []string{"nobody"}, Environment: "dev", Team: "payments", Action: Toggle}) {
		t.Error("an unknown group must be denied")
	}
	if set.Allowed(Request{Groups: nil, Environment: "dev", Team: "payments", Action: View}) {
		t.Error("no groups must be denied")
	}
}

func TestEmptyRuleSetDeniesEverything(t *testing.T) {
	set, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range AllActions {
		if set.Allowed(Request{Groups: []string{"anyone"}, Environment: "dev", Team: "x", Action: a}) {
			t.Errorf("empty config allowed %s", a)
		}
	}
}

func TestWildcardGrantsEveryAction(t *testing.T) {
	set := testSet(t)
	for _, a := range AllActions {
		if !set.Allowed(Request{Groups: []string{"flags-admins"}, Environment: "production", Team: "anything", Action: a}) {
			t.Errorf("admins should be allowed %s", a)
		}
	}
}

func TestFileScopingIsEnforced(t *testing.T) {
	set := testSet(t)

	if !set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "dev", Team: "payments", Action: EditRules}) {
		t.Error("payments team should edit their own file")
	}
	if set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "dev", Team: "growth", Action: EditRules}) {
		t.Error("payments team must not edit another team's file")
	}
}

func TestEnvironmentScopingIsEnforced(t *testing.T) {
	set := testSet(t)

	if !set.Allowed(Request{Groups: []string{"marketing"}, Environment: "production", Team: "growth", Action: Toggle}) {
		t.Error("marketing should toggle in production")
	}
	if set.Allowed(Request{Groups: []string{"marketing"}, Environment: "dev", Team: "growth", Action: Toggle}) {
		t.Error("marketing has no dev access")
	}
}

func TestActionScopingIsEnforced(t *testing.T) {
	set := testSet(t)
	base := Request{Groups: []string{"marketing"}, Environment: "production", Team: "growth"}

	for _, allowed := range []Action{Toggle, Rollout, View} {
		r := base
		r.Action = allowed
		if !set.Allowed(r) {
			t.Errorf("marketing should be allowed %s", allowed)
		}
	}

	for _, denied := range []Action{EditRules, EditVariations, Create, Delete} {
		r := base
		r.Action = denied
		if set.Allowed(r) {
			t.Errorf("marketing must not be allowed %s", denied)
		}
	}
}

func TestOmittingActionsGrantsAll(t *testing.T) {
	set := testSet(t)
	for _, a := range AllActions {
		if !set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "staging", Team: "payments", Action: a}) {
			t.Errorf("payments team omitted actions, so %s should be granted", a)
		}
	}
}

func TestViewIsImpliedByAnyOtherAction(t *testing.T) {
	set, err := New([]Rule{{Group: "toggler", Teams: []string{"x"}, Actions: []string{"toggle"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !set.Allowed(Request{Groups: []string{"toggler"}, Environment: "dev", Team: "x", Action: View}) {
		t.Error("being able to toggle implies being able to view")
	}
}

func TestTeamsMatchByNameOnly(t *testing.T) {
	for _, pattern := range []string{"pay*", "production/payments", "pay?", "[p]ayments"} {
		if _, err := New([]Rule{{Group: "team", Teams: []string{pattern}}}); err == nil {
			t.Errorf("%q must be rejected; teams are names, not patterns", pattern)
		}
	}
}

func TestAFlagWithNoTeamIsReachedOnlyByTheWildcard(t *testing.T) {
	set := testSet(t)
	if !set.Allowed(Request{Groups: []string{"flags-admins"}, Environment: "dev", Team: "", Action: Delete}) {
		t.Error(`a "*" rule must cover flags with no team`)
	}
	if set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "dev", Team: "", Action: View}) {
		t.Error("a named-team rule must not reach flags with no team")
	}
}

func TestActionsForReportsExactlyWhatIsGranted(t *testing.T) {
	set := testSet(t)

	got := set.ActionsFor([]string{"marketing"}, "production", "growth")
	want := map[Action]bool{View: true, Toggle: true, Rollout: true}
	if len(got) != len(want) {
		t.Fatalf("ActionsFor = %v, want %v", got, want)
	}
	for _, a := range got {
		if !want[a] {
			t.Errorf("unexpected action %s", a)
		}
	}
}

func TestVisibleEnvironments(t *testing.T) {
	set := testSet(t)
	envs := []string{"dev", "staging", "production"}

	if got := set.VisibleEnvironments([]string{"marketing"}, envs); len(got) != 1 || got[0] != "production" {
		t.Errorf("marketing environments = %v", got)
	}
	if got := set.VisibleEnvironments([]string{"payments-team"}, envs); len(got) != 3 {
		t.Errorf("payments environments = %v", got)
	}
	if got := set.VisibleEnvironments([]string{"stranger"}, envs); len(got) != 0 {
		t.Errorf("stranger should see nothing, got %v", got)
	}
}

func TestMultipleGroupsUnion(t *testing.T) {
	set := testSet(t)
	groups := []string{"marketing", "payments-team"}

	if !set.Allowed(Request{Groups: groups, Environment: "dev", Team: "payments", Action: Delete}) {
		t.Error("payments membership should grant delete on payments")
	}
	if !set.Allowed(Request{Groups: groups, Environment: "production", Team: "growth", Action: Toggle}) {
		t.Error("marketing membership should grant toggle on growth")
	}
	if set.Allowed(Request{Groups: groups, Environment: "production", Team: "growth", Action: Delete}) {
		t.Error("neither group grants delete on growth")
	}
}

func TestInvalidConfigIsRejected(t *testing.T) {
	if _, err := New([]Rule{{Group: "", Teams: []string{"*"}}}); err == nil {
		t.Error("a rule without a group should be rejected")
	}
	if _, err := New([]Rule{{Group: "g"}}); err == nil {
		t.Error("a rule without teams should be rejected")
	}
	if _, err := New([]Rule{{Group: "g", Teams: []string{"*"}, Actions: []string{"launch_missiles"}}}); err == nil {
		t.Error("an unknown action should be rejected at config load, not silently ignored")
	}
}

func TestEmptyActionIsDenied(t *testing.T) {
	set := testSet(t)
	if set.Allowed(Request{Groups: []string{"flags-admins"}, Environment: "dev", Team: "x"}) {
		t.Error("a request with no action must be denied")
	}
}

func TestCanCreateEnvironments(t *testing.T) {
	set, err := New([]Rule{
		{Group: "admins", Teams: []string{"*"}},
		{Group: "starred", Teams: []string{"*"}, Environments: []string{"*"}, Actions: []string{"create"}},
		{Group: "scoped", Teams: []string{"*"}, Environments: []string{"dev"}, Actions: []string{"create"}},
		{Group: "editors", Teams: []string{"*"}, Actions: []string{"toggle", "rollout"}},
		{Group: "viewers", Teams: []string{"*"}, Actions: []string{"view"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		groups []string
		want   bool
	}{
		{[]string{"admins"}, true},
		{[]string{"starred"}, true},
		{[]string{"scoped"}, false},
		{[]string{"editors"}, false},
		{[]string{"viewers"}, false},
		{[]string{"stranger"}, false},
		{nil, false},
		{[]string{"viewers", "admins"}, true},
	}
	for _, tc := range cases {
		if got := set.CanCreateEnvironments(tc.groups); got != tc.want {
			t.Errorf("CanCreateEnvironments(%v) = %v, want %v", tc.groups, got, tc.want)
		}
	}

	var empty *Set
	if empty.CanCreateEnvironments([]string{"admins"}) {
		t.Error("a nil set must deny")
	}
}

func TestCanCreateEnvironmentsHonoursTheWildcardGroup(t *testing.T) {
	set, err := New([]Rule{{Group: "*", Teams: []string{"*"}, Actions: []string{"create"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !set.CanCreateEnvironments([]string{"anyone"}) {
		t.Error(`group "*" matches every signed-in user`)
	}
}
