package tools

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/tool"
)

// ReadFileInput 定义读取文件工具的输入参数
type ReadFileInput struct {
	FilePath string `json:"file_path"` // 要读取的文件路径（相对于数据目录）
}

// ReadFileOutput 定义读取文件工具的输出结果
type ReadFileOutput struct {
	Content string `json:"content"`         // 文件内容
	Success bool   `json:"success"`         // 是否成功读取
	Error   string `json:"error,omitempty"` // 错误信息（如果有）
}

// 是一个读取本地文件的工具函数
// 用于读取存储在数据目录中的 trace 数据文件
func ReadFileTool(ctx tool.Context, input ReadFileInput) (ReadFileOutput, error) {
	// 记录工具调用日志到持久化文件（每次调用记录时间和入参，换行）
	logFile := "tool_calls.log"
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// 构建日志条目
	logEntry := map[string]interface{}{
		"timestamp": timestamp,
		"tool":      "read_trace_file",
		"input":     input,
	}

	// 将日志条目转换为 JSON
	logJSON, err := json.Marshal(logEntry)
	if err != nil {
		// 如果 JSON 序列化失败，使用简单格式
		logJSON = []byte(fmt.Sprintf(`{"timestamp":"%s","tool":"read_trace_file","input":{"file_path":"%s"}}`,
			timestamp, input.FilePath))
	}

	// 追加写入日志文件（如果文件不存在会自动创建），每次调用换行
	logFileHandle, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		logFileHandle.WriteString(string(logJSON) + "\n")
		logFileHandle.Close()
	}

	// 获取数据目录路径（可以通过环境变量配置，默认使用当前目录下的 data 目录）
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}

	// 构建完整文件路径
	fullPath := filepath.Join(dataDir, input.FilePath)

	// 安全检查：确保文件在数据目录内，防止路径遍历攻击
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return ReadFileOutput{
			Success: false,
			Error:   fmt.Sprintf("无法解析数据目录路径: %v", err),
		}, nil
	}

	absFilePath, err := filepath.Abs(fullPath)
	if err != nil {
		return ReadFileOutput{
			Success: false,
			Error:   fmt.Sprintf("无法解析文件路径: %v", err),
		}, nil
	}

	// 检查文件是否在数据目录内
	relPath, err := filepath.Rel(absDataDir, absFilePath)
	if err != nil || relPath == ".." || len(relPath) >= 2 && relPath[:2] == ".." {
		return ReadFileOutput{
			Success: false,
			Error:   "不允许访问数据目录之外的文件",
		}, nil
	}

	// 读取文件内容
	content, err := os.ReadFile(absFilePath)
	if err != nil {
		return ReadFileOutput{
			Success: false,
			Error:   fmt.Sprintf("读取文件失败: %v", err),
		}, nil
	}

	fmt.Println("--> 模型调用了read工具" + time.Now().Format("2006-01-02 15:04:05"))
	return ReadFileOutput{
		Content: string(content),
		Success: true,
	}, nil
}

func GetFileInputSchema() *jsonschema.Schema {
	inputSchema, err := jsonschema.For[ReadFileInput](nil)
	if err != nil {
		log.Fatalf("创建输入 Schema 失败: %v", err)
	}

	// 为 file_path 属性添加详细描述和示例，帮助模型理解如何正确调用工具
	if filePathProp, ok := inputSchema.Properties["file_path"]; ok {
		filePathProp.Description = "要读取的文件路径，相对于数据目录（data/）的相对路径。例如使用：'payment_service_logs.txt' 或 'trace_data/trace_12345.json'。不允许使用 '..' 等路径遍历符号。"
		filePathProp.Examples = []any{"payment_service_logs.txt", "trace_data/trace_12345.json", "logs/error_20240115.log"}
		// 确保 file_path 是必需字段
		if inputSchema.Required == nil {
			inputSchema.Required = []string{}
		}
		inputSchema.Required = append(inputSchema.Required, "file_path")
	}
	return inputSchema
}
