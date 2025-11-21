// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// 根因分析示例：演示如何使用 adk-go 的 openai 模型对接 KimiK2，
// 构建一个带工具的 agent 来分析 trace 数据并输出根因分析结论。
package main

//
//import (
//	"context"
//	"fmt"
//	"log"
//	"os"
//	"path/filepath"
//	"time"
//
//	"github.com/google/jsonschema-go/jsonschema"
//	agentA "google.golang.org/adk/agent"
//	"google.golang.org/adk/agent/llmagent"
//	"google.golang.org/adk/artifact"
//	"google.golang.org/adk/model/openai"
//	runnerA "google.golang.org/adk/runner"
//	sessionA "google.golang.org/adk/session"
//	"google.golang.org/adk/tool"
//	"google.golang.org/adk/tool/functiontool"
//	"google.golang.org/genai"
//)
//
//// ReadFileInput 定义读取文件工具的输入参数
//type ReadFileInput struct {
//	FilePath string `json:"file_path"` // 要读取的文件路径（相对于数据目录，例如：payment_service_logs.txt 或 trace_data/trace_12345.json）
//}
//
//// ReadFileOutput 定义读取文件工具的输出结果
//type ReadFileOutput struct {
//	Content string `json:"content"`         // 文件内容
//	Success bool   `json:"success"`         // 是否成功读取
//	Error   string `json:"error,omitempty"` // 错误信息（如果有）
//}
//
//// 是一个读取本地文件的工具函数
//// 用于读取存储在数据目录中的 trace 数据文件
//func readFileTool(ctx tool.Context, input ReadFileInput) (ReadFileOutput, error) {
//
//	// 获取数据目录路径（可以通过环境变量配置，默认使用当前目录下的 data 目录）
//	dataDir := os.Getenv("DATA_DIR")
//	if dataDir == "" {
//		dataDir = "data"
//	}
//
//	// 构建完整文件路径
//	fullPath := filepath.Join(dataDir, input.FilePath)
//
//	// 安全检查：确保文件在数据目录内，防止路径遍历攻击
//	absDataDir, err := filepath.Abs(dataDir)
//	if err != nil {
//		return ReadFileOutput{
//			Success: false,
//			Error:   fmt.Sprintf("无法解析数据目录路径: %v", err),
//		}, nil
//	}
//
//	absFilePath, err := filepath.Abs(fullPath)
//	if err != nil {
//		return ReadFileOutput{
//			Success: false,
//			Error:   fmt.Sprintf("无法解析文件路径: %v", err),
//		}, nil
//	}
//
//	// 检查文件是否在数据目录内
//	relPath, err := filepath.Rel(absDataDir, absFilePath)
//	if err != nil || relPath == ".." || len(relPath) >= 2 && relPath[:2] == ".." {
//		return ReadFileOutput{
//			Success: false,
//			Error:   "不允许访问数据目录之外的文件",
//		}, nil
//	}
//
//	// 读取文件内容
//	content, err := os.ReadFile(absFilePath)
//	if err != nil {
//		return ReadFileOutput{
//			Success: false,
//			Error:   fmt.Sprintf("读取文件失败: %v", err),
//		}, nil
//	}
//
//	fmt.Println("--> 模型调用了read工具" + time.Now().Format("2006-01-02 15:04:05"))
//	return ReadFileOutput{
//		Content: string(content),
//		Success: true,
//	}, nil
//}
//
//func main() {
//	ctx := context.Background()
//
//	// ========== 步骤 1: 配置 KimiK2 API ==========
//	// 从环境变量读取配置，如果没有设置则使用默认值（测试阶段）
//	baseURL := os.Getenv("KIMIK2_BASE_URL")
//	if baseURL == "" {
//		baseURL = "https://api.moonshot.cn/v1"
//	}
//
//	apiKey := os.Getenv("KIMIK2_API_KEY")
//	if apiKey == "" {
//		apiKey = "sk-4nGV86STuhZhzE55008lpNSwA4qx7JW1w0PsKSWjBhWOm7pN"
//	}
//
//	modelName := os.Getenv("KIMIK2_MODEL")
//	if modelName == "" {
//		modelName = "kimi-k2-0905-preview"
//	}
//
//	log.Printf("配置信息:")
//	log.Printf("  BaseURL: %s", baseURL)
//	log.Printf("  Model: %s", modelName)
//
//	// ========== 步骤 2: 创建 OpenAI 兼容的模型适配器 ==========
//	model, err := openai.NewModel(modelName, &openai.Config{
//		BaseURL: baseURL,
//		APIKey:  apiKey,
//
//		MaxRetries:       1,                 // 最大重试次数
//		MaxHistoryLength: 50,                // 最大历史消息数
//		DebugLogging:     true,              // 是否开启调试日志
//		Timeout:          120 * time.Second, // 请求超时时间
//		SessionTTL:       1 * time.Hour,     // Session TTL: 1小时
//	})
//	if err != nil {
//		log.Fatalf("创建模型失败: %v", err)
//	}
//
//	log.Println("✓ 模型创建成功")
//
//	// ========== 步骤 3: 创建读取文件的工具 ==========
//	// 创建输入参数的 JSON Schema，添加详细的属性描述以便模型更好地理解如何调用
//	inputSchema, err := jsonschema.For[ReadFileInput](nil)
//	if err != nil {
//		log.Fatalf("创建输入 Schema 失败: %v", err)
//	}
//
//	// 为 file_path 属性添加详细描述和示例，帮助模型理解如何正确调用工具
//	if filePathProp, ok := inputSchema.Properties["file_path"]; ok {
//		filePathProp.Description = "要读取的文件路径，相对于数据目录（data/）的相对路径。例如使用：'payment_service_logs.txt' 或 'trace_data/trace_12345.json'。不允许使用 '..' 等路径遍历符号。"
//		filePathProp.Examples = []any{"payment_service_logs.txt", "trace_data/trace_12345.json", "logs/error_20240115.log"}
//		// 确保 file_path 是必需字段
//		if inputSchema.Required == nil {
//			inputSchema.Required = []string{}
//		}
//		inputSchema.Required = append(inputSchema.Required, "file_path")
//	}
//
//	readFileToolInstance, err := functiontool.New(
//		functiontool.Config{
//			Name:        "read_trace_file",
//			Description: "读取存储在数据目录中的 trace 数据文件。用于获取 trace 的上下游链路信息和异常数据。文件路径应该是相对于数据目录的相对路径（例如：payment_service_logs.txt）。",
//			InputSchema: inputSchema,
//		},
//		readFileTool,
//	)
//	if err != nil {
//		log.Fatalf("创建文件读取工具失败: %v", err)
//	}
//
//	log.Println("✓ 文件读取工具创建成功")
//
//	// ========== 步骤 4: 创建 LLM Agent ==========
//	agent, err := llmagent.New(llmagent.Config{
//		Name:        "root_cause_analyzer",
//		Model:       model,
//		Description: "一个专业的根因分析助手，能够分析 trace 数据，识别异常，并输出根因分析结论",
//		Instruction: `你是一个专业的根因分析专家。你的任务是：
//
//1. 分析用户提供的 trace 数据（包括上下游链路和异常信息）
//2. 识别关键异常点和潜在问题
//3. 使用 read_trace_file 工具读取相关的 trace 数据文件（如果用户提供了文件路径）
//4. 综合分析 trace 的调用链路、时间序列、错误信息等
//5. 输出清晰的根因分析结论，包括：
//   - 问题定位：哪个服务/模块出现了问题
//   - 根本原因：导致问题的根本原因
//   - 影响范围：问题影响了哪些下游服务
//   - 建议措施：如何修复和预防类似问题
//
//请使用专业但易懂的语言，确保分析结论准确且有价值。`,
//		Tools: []tool.Tool{
//			readFileToolInstance,
//		},
//	})
//	if err != nil {
//		log.Fatalf("创建 Agent 失败: %v", err)
//	}
//
//	log.Println("✓ Agent 创建成功")
//
//	// ========== 步骤 5: 创建 Session 和 Runner ==========
//	userID := "root_cause_user"
//	appName := "root_cause_analysis"
//
//	sessionService := sessionA.InMemoryService()
//	resp, err := sessionService.Create(ctx, &sessionA.CreateRequest{
//		AppName: appName,
//		UserID:  userID,
//	})
//	if err != nil {
//		log.Fatalf("创建 Session 失败: %v", err)
//	}
//
//	session := resp.Session
//	log.Printf("✓ Session 创建成功: %s", session.ID())
//
//	artifactService := artifact.InMemoryService()
//
//	runner, err := runnerA.New(runnerA.Config{
//		AppName:         appName,
//		Agent:           agent,
//		SessionService:  sessionService,
//		ArtifactService: artifactService,
//	})
//	if err != nil {
//		log.Fatalf("创建 Runner 失败: %v", err)
//	}
//
//	log.Println("✓ Runner 创建成功")
//
//	/**
//
//	 */
//	// ========== 步骤 6: 构建用户请求（模拟 trace 数据分析请求）==========
//	// 这里模拟一个根因分析的场景：用户提供 trace 信息，要求分析问题
//	userPrompt := `请读取 payment_service_logs.txt 文件获取更多详细信息，然后给出根因分析结论。`
//
//	log.Println("\n========== 开始执行 Agent ==========")
//	log.Printf("用户输入: %s\n", userPrompt)
//
//	// ========== 步骤 7: 调用 Agent 并处理响应 ==========
//	userMsg := genai.NewContentFromText(userPrompt, genai.RoleUser)
//
//	fmt.Println("\n========== Agent 响应 ==========")
//	var finalResponse string
//	eventCount := 0
//
//	// 使用非流式模式执行（也可以使用 StreamingModeSSE 进行流式输出）
//	for event, err := range runner.Run(ctx, userID, session.ID(), userMsg, agentA.RunConfig{
//		StreamingMode: agentA.StreamingModeNone,
//	}) {
//		if err != nil {
//			log.Printf("执行错误: %v", err)
//			continue
//		}
//
//		eventCount++
//
//		// 处理 LLM 响应事件
//		if event.LLMResponse.Content != nil {
//			for _, part := range event.LLMResponse.Content.Parts {
//				if part.Text != "" {
//					finalResponse += part.Text
//					fmt.Print(part.Text)
//				}
//			}
//		}
//
//	}
//
//	fmt.Println("\n\n========== 执行完成 ==========")
//	log.Printf("处理了 %d 个事件", eventCount)
//	log.Printf("最终响应长度: %d 字符", len(finalResponse))
//
//}
