package main

import (
	"context"
	"fmt"
	"log"
	"os"

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

func main() {
	ctx := context.Background()

	baseURL := os.Getenv("KIMIK2_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.moonshot.cn/v1"
	}
	apiKey := os.Getenv("KIMIK2_API_KEY")
	modelName := os.Getenv("KIMIK2_MODEL")
	if modelName == "" {
		modelName = "kimi-k2-0905-preview"
	}

	// 创建 logger 用于调试
	//logger := log.New(os.Stdout, "[DEBUG] ", log.LstdFlags)

	model, err := openai.NewModel(modelName, &openai.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		//DebugLogging: true, // 启用调试日志
		//Logger:       logger,
	})
	if err != nil {
		log.Fatalf("创建模型失败: %v", err)
	}

	readFileToolInstance, err := functiontool.New(
		functiontool.Config{
			Name:        "read_trace_file",
			Description: "读取存储在数据目录中的文件。这是唯一可以读取文件的方式。当用户要求读取文件时，你必须调用此工具。文件路径参数 file_path 应该是相对于数据目录的相对路径，例如：'payment_service_logs.txt'。",
			InputSchema: tools.GetFileInputSchema(),
		},
		tools.ReadFileTool,
	)
	if err != nil {
		log.Fatalf("创建文件读取工具失败: %v", err)
	}

	agent, err := llmagent.New(llmagent.Config{
		Name:        "read_file_test",
		Model:       model,
		Description: "一个专门用于读取文件的助手",
		Instruction: `你是一个文件读取助手。重要规则：
1. 当用户要求读取文件时，你必须使用 read_trace_file 工具来读取文件
2. 不要尝试直接读取文件或告诉用户文件不存在，必须先调用工具
3. 调用工具后，将工具返回的文件内容展示给用户
4. 如果工具返回错误，将错误信息告知用户

用户现在要求读取文件，请立即调用 read_trace_file 工具。`,
		Tools: []tool.Tool{
			readFileToolInstance,
		},
	})
	if err != nil {
		log.Fatalf("创建 Agent 失败: %v", err)
	}

	sessionService := sessionA.InMemoryService()
	resp, err := sessionService.Create(ctx, &sessionA.CreateRequest{
		AppName: "read_test",
		UserID:  "test_user",
	})
	if err != nil {
		log.Fatalf("创建 Session 失败: %v", err)
	}

	runner, err := runnerA.New(runnerA.Config{
		AppName:         "read_test",
		Agent:           agent,
		SessionService:  sessionService,
		ArtifactService: artifact.InMemoryService(),
	})
	if err != nil {
		log.Fatalf("创建 Runner 失败: %v", err)
	}

	userMsg := genai.NewContentFromText("请读取 payment_service_logs.txt 文件的内容", genai.RoleUser)

	fmt.Println("========== Agent 响应 ==========")
	for event, err := range runner.Run(ctx, "test_user", resp.Session.ID(), userMsg, agentA.RunConfig{
		StreamingMode: agentA.StreamingModeNone,
	}) {
		if err != nil {
			log.Printf("执行错误: %v", err)
			continue
		}

		// 打印工具调用事件（用于调试）
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				// 检查是否是函数调用（工具调用）
				if part.FunctionCall != nil {
					fmt.Printf("\n[工具调用] %s: %v\n", part.FunctionCall.Name, part.FunctionCall.Args)
				}
				// 检查是否是函数响应（工具响应）
				if part.FunctionResponse != nil {
					fmt.Printf("\n[工具响应] %s: %v\n", part.FunctionResponse.Name, part.FunctionResponse.Response)
				}
				// 打印文本响应
				if part.Text != "" {
					fmt.Print(part.Text)
				}
			}
		}
	}

	fmt.Println("\n========== 完成 ==========")
}
