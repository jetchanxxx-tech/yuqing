package llm

import "context"

// FakeSummaryText is the canned assistant content returned by FakeProvider.
// It is a deterministic summary for the demo subject: 广汽本田雅阁后排体验舆情.
const FakeSummaryText = "近期全网涉\"雅阁后排\"讨论共监测到 3260 条相关内容，声量环比上升 18%。" +
	"话题焦点集中在后排空间表现、座椅舒适度与静谧性。正面评价占 62%，主要围绕加长轴距带来的" +
	"腿部空间改善与座椅包裹性提升；负面评价占 11%，集中在坐垫长度与长途乘坐疲劳感；中性讨论占 27%。" +
	"热门来源为微博汽车博主评测与社区口碑帖。综合研判：雅阁后排整体口碑向好，\"空间越级\"认知正在强化，建议持续监测夏季长途用车体验反馈。"

// FakeProvider is a deterministic Provider implementation for tests and local
// demos. Chat never calls an external API — it returns a fixed response with
// fixed usage tokens, so budgets and metering can be exercised offline.
type FakeProvider struct{}

var _ Provider = FakeProvider{}

// Chat returns the canned 雅阁后排 summary with Usage{420 prompt, 180 completion}.
func (FakeProvider) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{
		ID:    "chatcmpl-fake-0001",
		Model: req.Model,
		Usage: Usage{
			PromptTokens:     420,
			CompletionTokens: 180,
			TotalTokens:      600,
		},
		Choices: []Choice{
			{
				Index:   0,
				Message: Message{Role: "assistant", Content: FakeSummaryText},
			},
		},
	}, nil
}
