package tools_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jiatianzhao/adk-go-openai/examples/root_cause_analysis/tools"
)

func TestWriteFileTool(t *testing.T) {

	input := tools.WriteFileInput{
		"report/唐诗", "我测试的", "text",
	}
	// 获取数据目录路径
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}

	// 构建完整文件路径
	fullPath := filepath.Join(dataDir, input.FilePath)

	//// 安全检查：确保文件在数据目录内
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		fmt.Sprintf("无法解析数据目录路径: %v", err)
	}

	absFilePath, err := filepath.Abs(fullPath)
	if err != nil {
		fmt.Sprintf("无法解析文件路径: %v", err)
	}

	// 检查文件是否在数据目录内
	relPath, err := filepath.Rel(absDataDir, absFilePath)
	if err != nil || relPath == ".." || len(relPath) >= 2 && relPath[:2] == ".." {
		fmt.Errorf("不允许写入数据目录之外的文件")
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(absFilePath), 0755); err != nil {
		fmt.Sprintf("创建目录失败: %v", err)
	}

	// 确定文件格式
	format := input.Format
	if format == "" {
		ext := strings.ToLower(filepath.Ext(input.FilePath))
		switch ext {
		case ".json":
			format = "json"
		case ".csv":
			format = "csv"
		default:
			format = "text"
		}
	}

	// 转换内容为字节
	var contentBytes []byte
	switch format {
	case "json":
		// JSON格式
		jsonBytes, err := json.MarshalIndent(input.Content, "", "  ")
		if err != nil {

			fmt.Sprintf("JSON序列化失败: %v", err)
		}
		contentBytes = jsonBytes

	case "csv":
		// CSV格式（简化实现，假设是map数组）
		if arr, ok := input.Content.([]interface{}); ok && len(arr) > 0 {
			var csvLines []string
			// 获取第一行的键作为表头
			if firstRow, ok := arr[0].(map[string]interface{}); ok {
				headers := make([]string, 0, len(firstRow))
				for k := range firstRow {
					headers = append(headers, k)
				}
				csvLines = append(csvLines, strings.Join(headers, ","))

				// 添加数据行
				for _, row := range arr {
					if rowMap, ok := row.(map[string]interface{}); ok {
						values := make([]string, 0, len(headers))
						for _, h := range headers {
							val := fmt.Sprintf("%v", rowMap[h])
							// CSV转义：如果包含逗号或引号，需要用引号包裹
							if strings.Contains(val, ",") || strings.Contains(val, `"`) {
								val = `"` + strings.ReplaceAll(val, `"`, `""`) + `"`
							}
							values = append(values, val)
						}
						csvLines = append(csvLines, strings.Join(values, ","))
					}
				}
				contentBytes = []byte(strings.Join(csvLines, "\n"))
			} else {

				fmt.Printf("CSV格式要求内容为map数组")
			}
		}

	default:
		// 文本格式
		if str, ok := input.Content.(string); ok {
			contentBytes = []byte(str)
		} else {
			// 尝试转换为字符串
			contentBytes = []byte(fmt.Sprintf("%v", input.Content))
		}
	}

	// 写入文件
	if err := os.WriteFile(absFilePath, contentBytes, 0644); err != nil {
		fmt.Sprintf("写入文件失败: %v", err)
	}

	fmt.Println(absFilePath)
	fmt.Println("--> 模型调用了write_file工具" + time.Now().Format("2006-01-02 15:04:05"))
	return
}
