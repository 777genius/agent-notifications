package portablesetup

import (
	"errors"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
)

func TestPersistResultPreservesIncompleteCommit(t *testing.T) {
	inner := errors.New("host seam refused after managed commit")
	err := persistResult(uapinstaller.Result{
		Outcome: uapinstaller.OutcomeIncomplete,
		Client: uapinstaller.ClientResult{
			ClientID:        "codex",
			Materialization: string(domain.MaterializationMaterialized),
			Activation:      string(domain.ActivationFailed),
		},
		Binding: uapinstaller.BindingFacts{BindingID: "bound"},
	}, inner)
	var got ResultError
	if !errors.As(err, &got) || !errors.Is(err, inner) {
		t.Fatalf("result error: %v", err)
	}
	if got.Result.Outcome != uapinstaller.OutcomeIncomplete {
		t.Fatalf("outcome: %+v", got.Result)
	}
	if got.Result.Client.Materialization != string(domain.MaterializationMaterialized) {
		t.Fatalf("materialization: %+v", got.Result.Client)
	}
	if got.Result.Client.Activation != string(domain.ActivationFailed) {
		t.Fatalf("activation: %+v", got.Result.Client)
	}
}

func TestPersistResultLeavesEmptyResultUnwrapped(t *testing.T) {
	inner := ErrPreflight
	err := persistResult(uapinstaller.Result{}, inner)
	if !errors.Is(err, inner) {
		t.Fatalf("empty result: %v", err)
	}
	var got ResultError
	if errors.As(err, &got) {
		t.Fatalf("empty result wrapped: %+v", got)
	}
}
