package llm

import (
	"context"
	"strings"
	"testing"
)

func TestFakeProvider_ChatReturnsCannedResponse(t *testing.T) {
	p := FakeProvider{}
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "总结近期雅阁后排舆情"}},
	})
	if err != nil {
		t.Fatalf("FakeProvider.Chat returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a ChatResponse, got nil")
	}
	if resp.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want %q (request model echoed)", resp.Model, "deepseek-chat")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("choice role = %q, want assistant", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].Message.Content == "" {
		t.Error("expected non-empty assistant content from FakeProvider")
	}
}

func TestFakeProvider_ChatFixedUsageTokens(t *testing.T) {
	p := FakeProvider{}
	resp, err := p.Chat(context.Background(), ChatRequest{Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("FakeProvider.Chat returned error: %v", err)
	}
	if resp.Usage.PromptTokens != 420 {
		t.Errorf("PromptTokens = %d, want 420", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 180 {
		t.Errorf("CompletionTokens = %d, want 180", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 600 {
		t.Errorf("TotalTokens = %d, want 600", resp.Usage.TotalTokens)
	}
}

func TestFakeProvider_ContentDescribesYageRearSeats(t *testing.T) {
	p := FakeProvider{}
	resp, err := p.Chat(context.Background(), ChatRequest{Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("FakeProvider.Chat returned error: %v", err)
	}
	content := resp.Choices[0].Message.Content
	if !strings.Contains(content, "雅阁后排") {
		t.Errorf("content should mention the demo subject 雅阁后排, got: %s", content)
	}
}

func TestFakeProvider_NeedsNoExternalAPI(t *testing.T) {
	// Compile-time contract: FakeProvider satisfies the Provider interface,
	// so it can be wired anywhere a real provider is expected.
	var _ Provider = FakeProvider{}
}
