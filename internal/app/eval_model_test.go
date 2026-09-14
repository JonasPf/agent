package app

import (
	"strings"
	"testing"
)

// The default eval model is a real model named in the source, and one day it
// will stop existing: a provider withdraws a tier and every case fails at once,
// on a rejection from the gateway that reads as a broken suite rather than a
// missing model. That is how the last one was found to be gone, after a while
// spent looking at the cases.
//
// So the run checks first, and says the one useful thing.
func TestAnEvalModelThatIsNotThereIsNamedAsTheProblem(t *testing.T) {
	available := []ModelInfo{{ID: "google/gemma-4-26b-a4b-it"}, {ID: "openai/gpt-4o-mini"}}

	if err := checkEvalModel("minimax/minimax-m3:free", available); err == nil {
		t.Fatal("a model that is not available was accepted")
	} else {
		for _, want := range []string{"minimax/minimax-m3:free", "AGENT_EVAL_MODEL"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error does not mention %q: %v", want, err)
			}
		}
	}

	if err := checkEvalModel("google/gemma-4-26b-a4b-it", available); err != nil {
		t.Errorf("a model that is available was rejected: %v", err)
	}
}

// A catalogue that could not be fetched is not evidence that the model is gone.
// Refusing to run on the strength of a failed lookup would turn a flaky network
// into a broken suite, which is the opposite of what this check is for.
func TestAnUnreadableCatalogueDoesNotBlockTheRun(t *testing.T) {
	if err := checkEvalModel("anything/at-all", nil); err != nil {
		t.Errorf("an empty catalogue blocked the run: %v", err)
	}
}
