package tools_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	agentA "github.com/jiatianzhao/adk-go-openai/agent"
	"github.com/jiatianzhao/adk-go-openai/agent/llmagent"
	"github.com/jiatianzhao/adk-go-openai/artifact"
	"github.com/jiatianzhao/adk-go-openai/examples/root_cause_analysis/tools"
	"github.com/jiatianzhao/adk-go-openai/model/openai"
	runnerA "github.com/jiatianzhao/adk-go-openai/runner"
	sessionA "github.com/jiatianzhao/adk-go-openai/session"
	"github.com/jiatianzhao/adk-go-openai/tool"
	"github.com/jiatianzhao/adk-go-openai/tool/functiontool"
	"google.golang.org/genai"
)

func TestA(t *testing.T) {
	ctx := context.Background()

	baseURL := "https://api.moonshot.cn/v1"
	apiKey := os.Getenv("KIMIK2_API_KEY")
	modelName := "kimi-k2-0905-preview"
	// ========== 步骤 2: 创建 OpenAI 兼容的模型适配器 ==========
	model, err := openai.NewModel(modelName, &openai.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,

		MaxRetries:       1,                 // 最大重试次数
		MaxHistoryLength: 50,                // 最大历史消息数
		DebugLogging:     true,              // 是否开启调试日志
		Timeout:          120 * time.Second, // 请求超时时间
		SessionTTL:       1 * time.Hour,     // Session TTL: 1小时
	})
	if err != nil {
		log.Fatalf("创建模型失败: %v", err)
	}

	writeFileToolInstance, err := functiontool.New(
		functiontool.Config{
			Name:        "write_analysis_report",
			Description: "将结果等写入文件。支持文本、JSON、CSV 等格式。文件会保存在数据目录中，文件路径应该是相对于数据目录的相对路径。",
		},
		tools.WriteFileTool,
	)
	if err != nil {
		log.Fatalf("创建文件写入工具失败: %v", err)
	}

	agent, err := llmagent.New(llmagent.Config{
		Name:        "tang",
		Model:       model,
		Description: "你是一个唐代诗人",
		Instruction: "你需要完成诗词创作，并使用 write_analysis_report 工具将结果保存到文件(例如：reports/唐诗.txt)",
		Tools: []tool.Tool{
			writeFileToolInstance,
		},
	})

	userID := "root_cause_user"
	appName := "root_cause_analysis"

	sessionService := sessionA.InMemoryService()
	resp, err := sessionService.Create(ctx, &sessionA.CreateRequest{
		AppName: appName,
		UserID:  userID,
	})
	if err != nil {
		log.Fatalf("创建 Session 失败: %v", err)
	}

	session := resp.Session
	log.Printf("✓ Session 创建成功: %s", session.ID())

	artifactService := artifact.InMemoryService()

	runner, err := runnerA.New(runnerA.Config{
		AppName:         appName,
		Agent:           agent,
		SessionService:  sessionService,
		ArtifactService: artifactService,
	})
	if err != nil {
		log.Fatalf("创建 Runner 失败: %v", err)
	}

	log.Println("✓ Runner 创建成功")

	userPrompt := "写一首李白的诗,必须调用 write_analysis_report 工具保存结果,并告知我你保存的路径"

	log.Println("\n========== 开始执行 Agent ==========")
	log.Printf("用户输入: %s\n", userPrompt)

	// ========== 步骤 7: 调用 Agent 并处理响应 ==========
	userMsg := genai.NewContentFromText(userPrompt, genai.RoleUser)

	fmt.Println("\n========== Agent 响应 ==========")
	var finalResponse string
	eventCount := 0

	// 使用非流式模式执行（也可以使用 StreamingModeSSE 进行流式输出）
	for event, err := range runner.Run(ctx, userID, session.ID(), userMsg, agentA.RunConfig{
		StreamingMode: agentA.StreamingModeNone,
	}) {
		if err != nil {
			log.Printf("执行错误: %v", err)
			continue
		}

		eventCount++

		// 处理 LLM 响应事件
		if event.LLMResponse.Content != nil {
			for _, part := range event.LLMResponse.Content.Parts {
				if part.Text != "" {
					finalResponse += part.Text
					fmt.Print(part.Text)
				}
			}
		}

	}

	fmt.Println("\n\n========== 执行完成 ==========")
	log.Printf("处理了 %d 个事件", eventCount)
	log.Printf("最终响应长度: %d 字符", len(finalResponse))

}
