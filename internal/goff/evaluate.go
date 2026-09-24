package goff

import (
	"context"
	"fmt"
	"sync"

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
