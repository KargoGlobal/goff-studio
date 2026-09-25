package goff

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-feature-flag/studio/pkg/splits"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
)

type memoryRetriever struct{ body []byte }

func (m *memoryRetriever) Retrieve(_ context.Context) ([]byte, error) { return m.body, nil }

var evalMu sync.Mutex

func (a *Adapter) Evaluate(content []byte, flagKey, targetingKey string, attrs map[string]any) EvalResult {
	if err := a.Validate(content); err != nil {
		return EvalResult{Error: err.Error()}
	}

	evalMu.Lock()
	defer evalMu.Unlock()

	client, err := ffclient.New(ffclient.Config{
		Retriever:  &memoryRetriever{body: content},
		FileFormat: "yaml",
	})
	if err != nil {
		return EvalResult{Error: fmt.Sprintf("preview engine: %s", err)}
	}
	defer client.Close()

	builder := ffcontext.NewEvaluationContextBuilder(targetingKey)
	for k, v := range attrs {
		builder.AddCustom(k, v)
	}
	ctx := builder.Build()

	raw, err := client.RawVariation(flagKey, ctx, nil)
	if err != nil {
		return EvalResult{Error: err.Error(), Reason: raw.Reason}
	}

	return EvalResult{
		Variation: raw.VariationType,
		Value:     raw.Value,
		Reason:    raw.Reason,
	}
}

// EvaluateSplits previews a flag that carries metadata.experiment with the
// shard evaluator services use, since GO Feature Flag itself ignores the block.
func (a *Adapter) EvaluateSplits(content []byte, flagKey, targetingKey string, attrs map[string]any, now time.Time) (EvalResult, bool) {
	flags, _, err := a.Parse("preview", content)
	if err != nil {
		return EvalResult{Error: err.Error()}, true
	}
	var target *Flag
	for i := range flags {
		if flags[i].Key == flagKey {
			target = &flags[i]
		}
	}
	if target == nil {
		return EvalResult{Error: fmt.Sprintf("flag %q not found", flagKey)}, true
	}
	if target.Experiment == nil && target.ExperimentError == "" {
		return EvalResult{}, false
	}
	if target.ExperimentError != "" {
		return EvalResult{Error: "the experiment block cannot be read: " + target.ExperimentError}, true
	}
	if err := a.Validate(content); err != nil {
		return EvalResult{Error: err.Error()}, true
	}

	internal := target.Internal()
	ev, err := splits.New(splits.FromGOFF(flagKey, internal), target.Experiment)
	if err != nil {
		return EvalResult{Error: err.Error()}, true
	}
	got := ev.EvaluateAt(now, targetingKey, attrs)
	result := EvalResult{
		Variation:     got.Variation,
		Reason:        got.Reason,
		ExperimentKey: got.ExperimentKey,
		Allocation:    got.Allocation,
		DoLog:         &got.DoLog,
		ExtraLogging:  got.ExtraLogging,
	}
	if got.Variation != "" {
		result.Value = internal.GetVariationValue(got.Variation)
	}
	return result, true
}
