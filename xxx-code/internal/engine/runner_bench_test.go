package engine

import (
	"context"
	"testing"
)

func BenchmarkRunnerRunTurn(b *testing.B) {
	provider := &stubProvider{}
	runner := NewRunner(provider, NewRegistry(&echoTool{}), RunnerConfig{
		Model:        "test-model",
		SystemPrompt: "test",
		MaxTurns:     4,
	})

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		provider.calls = 0
		session := NewSession()
		if _, err := runner.RunTurn(context.Background(), session, "benchmark turn"); err != nil {
			b.Fatal(err)
		}
	}
}
