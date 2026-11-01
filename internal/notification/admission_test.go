package notification

import "testing"

func TestAssessDeliveryPreservesNativeAdmissionContract(t *testing.T) {
	valid := PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}
	for _, tc := range []struct {
		name       string
		request    Request
		wantNav    Navigation
		wantStatus string
		wantReason string
	}{
		{"default_required", Request{Policy: valid}, Required, "", ""},
		{"explicit_best_effort", Request{Navigation: BestEffort, Policy: valid}, BestEffort, "", ""},
		{"explicit_none", Request{Navigation: None, Policy: valid}, None, "", ""},
		{"unknown_navigation", Request{Navigation: "maybe", Policy: valid}, "maybe", "rejected", "malformed_request"},
		{"invalid_config", Request{Navigation: None, Policy: PolicySnapshot{ExplicitEnabled: true, DesktopEnabled: true}}, None, "rejected", "configuration_invalid"},
		{"not_consented", Request{Navigation: None, Policy: PolicySnapshot{Valid: true, DesktopEnabled: true}}, None, "suppressed", "disabled"},
		{"desktop_disabled", Request{Navigation: BestEffort, Policy: PolicySnapshot{Valid: true, ExplicitEnabled: true}}, BestEffort, "suppressed", "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessDelivery(tc.request)
			if got.Navigation != tc.wantNav || got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("AssessDelivery(%s) = %+v; want navigation=%s status=%q reason=%q",
					tc.name, got, tc.wantNav, tc.wantStatus, tc.wantReason)
			}
		})
	}
}

func TestAssessDeliveryDoesNotRequireOrInventTarget(t *testing.T) {
	r := Request{Navigation: BestEffort, Policy: PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: false}}
	decision := AssessDelivery(r)
	if decision.Status != "" || decision.Navigation != BestEffort || r.Target != (DesktopTarget{}) {
		t.Fatalf("policy must not manufacture target authority: decision=%+v target=%+v", decision, r.Target)
	}
}
