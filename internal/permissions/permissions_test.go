package permissions

import "testing"

func testSet(t *testing.T) *Set {
	t.Helper()
	set, err := New([]Rule{
		{Group: "flags-admins", Allow: []string{"*"}},
		{Group: "payments-team", Allow: []string{"payments"}, Environments: []string{"dev", "staging", "production"}},
		{Group: "marketing", Allow: []string{"growth"}, Environments: []string{"production"}, Actions: []string{"toggle", "rollout"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestDefaultDenyWithNoMatchingRule(t *testing.T) {
	set := testSet(t)

	if set.Allowed(Request{Groups: []string{"nobody"}, Environment: "dev", File: "payments.goff.yaml", Action: Toggle}) {
		t.Error("an unknown group must be denied")
	}
	if set.Allowed(Request{Groups: nil, Environment: "dev", File: "payments.goff.yaml", Action: View}) {
		t.Error("no groups must be denied")
	}
}

func TestEmptyRuleSetDeniesEverything(t *testing.T) {
	set, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range AllActions {
		if set.Allowed(Request{Groups: []string{"anyone"}, Environment: "dev", File: "x.yaml", Action: a}) {
			t.Errorf("empty config allowed %s", a)
		}
	}
}

func TestWildcardGrantsEveryAction(t *testing.T) {
	set := testSet(t)
	for _, a := range AllActions {
		if !set.Allowed(Request{Groups: []string{"flags-admins"}, Environment: "production", File: "anything.goff.yaml", Action: a}) {
			t.Errorf("admins should be allowed %s", a)
		}
	}
}

func TestFileScopingIsEnforced(t *testing.T) {
	set := testSet(t)

	if !set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "dev", File: "payments.goff.yaml", Action: EditRules}) {
		t.Error("payments team should edit their own file")
	}
	if set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "dev", File: "growth.goff.yaml", Action: EditRules}) {
		t.Error("payments team must not edit another team's file")
	}
}

func TestEnvironmentScopingIsEnforced(t *testing.T) {
	set := testSet(t)

	if !set.Allowed(Request{Groups: []string{"marketing"}, Environment: "production", File: "growth.goff.yaml", Action: Toggle}) {
		t.Error("marketing should toggle in production")
	}
	if set.Allowed(Request{Groups: []string{"marketing"}, Environment: "dev", File: "growth.goff.yaml", Action: Toggle}) {
		t.Error("marketing has no dev access")
	}
}

func TestActionScopingIsEnforced(t *testing.T) {
	set := testSet(t)
	base := Request{Groups: []string{"marketing"}, Environment: "production", File: "growth.goff.yaml"}

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
		if !set.Allowed(Request{Groups: []string{"payments-team"}, Environment: "staging", File: "payments.goff.yaml", Action: a}) {
			t.Errorf("payments team omitted actions, so %s should be granted", a)
		}
	}
}

func TestViewIsImpliedByAnyOtherAction(t *testing.T) {
	set, err := New([]Rule{{Group: "toggler", Allow: []string{"x"}, Actions: []string{"toggle"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !set.Allowed(Request{Groups: []string{"toggler"}, Environment: "dev", File: "x.goff.yaml", Action: View}) {
		t.Error("being able to toggle implies being able to view")
	}
}

func TestGlobPatterns(t *testing.T) {
	set, err := New([]Rule{{Group: "team", Allow: []string{"pay*"}}})
	if err != nil {
		t.Fatal(err)
	}

	if !set.Allowed(Request{Groups: []string{"team"}, Environment: "dev", File: "payments.goff.yaml", Action: View}) {
		t.Error("pay* should match payments")
	}
	if set.Allowed(Request{Groups: []string{"team"}, Environment: "dev", File: "growth.goff.yaml", Action: View}) {
		t.Error("pay* must not match growth")
	}
}

func TestPathPatterns(t *testing.T) {
	set, err := New([]Rule{{Group: "team", Allow: []string{"production/payments*"}}})
	if err != nil {
		t.Fatal(err)
	}

	if !set.Allowed(Request{Groups: []string{"team"}, Environment: "production", File: "production/payments.goff.yaml", Action: View}) {
		t.Error("full path pattern should match")
	}
	if set.Allowed(Request{Groups: []string{"team"}, Environment: "dev", File: "dev/payments.goff.yaml", Action: View}) {
		t.Error("pattern scoped to production must not match dev path")
	}
}

func TestActionsForReportsExactlyWhatIsGranted(t *testing.T) {
	set := testSet(t)

	got := set.ActionsFor([]string{"marketing"}, "production", "growth.goff.yaml")
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

	if !set.Allowed(Request{Groups: groups, Environment: "dev", File: "payments.goff.yaml", Action: Delete}) {
		t.Error("payments membership should grant delete on payments")
	}
	if !set.Allowed(Request{Groups: groups, Environment: "production", File: "growth.goff.yaml", Action: Toggle}) {
		t.Error("marketing membership should grant toggle on growth")
	}
	if set.Allowed(Request{Groups: groups, Environment: "production", File: "growth.goff.yaml", Action: Delete}) {
		t.Error("neither group grants delete on growth")
	}
}

func TestInvalidConfigIsRejected(t *testing.T) {
	if _, err := New([]Rule{{Group: "", Allow: []string{"*"}}}); err == nil {
		t.Error("a rule without a group should be rejected")
	}
	if _, err := New([]Rule{{Group: "g"}}); err == nil {
		t.Error("a rule without allow patterns should be rejected")
	}
	if _, err := New([]Rule{{Group: "g", Allow: []string{"*"}, Actions: []string{"launch_missiles"}}}); err == nil {
		t.Error("an unknown action should be rejected at config load, not silently ignored")
	}
}

func TestEmptyActionIsDenied(t *testing.T) {
	set := testSet(t)
	if set.Allowed(Request{Groups: []string{"flags-admins"}, Environment: "dev", File: "x.yaml"}) {
		t.Error("a request with no action must be denied")
	}
}
