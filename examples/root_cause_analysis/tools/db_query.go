package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jiatianzhao/adk-go-openai/tool"
)

// QueryDBTool 是一个查询数据库的工具函数
// 用于查询 trace 数据库获取调用记录、错误日志、服务指标等信息
func QueryDBTool(ctx tool.Context, input QueryDBInput) (QueryDBOutput, error) {
	// 安全检查：只允许 SELECT 查询
	sqlUpper := strings.TrimSpace(strings.ToUpper(input.SQL))
	if !strings.HasPrefix(sqlUpper, "SELECT") {
		return QueryDBOutput{
			Success: false,
			Error:   "只允许执行 SELECT 查询，不允许执行 INSERT、UPDATE、DELETE 等操作",
		}, nil
	}

	// 获取数据库连接配置
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}

	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "3306"
	}

	dbUser := os.Getenv("DB_USER")
	if dbUser == "" {
		dbUser = "root"
	}

	dbPassword := os.Getenv("DB_PASSWORD")
	if dbPassword == "" {
		dbPassword = "Jia011206"
	}

	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "error"
	}

	// 构建数据库连接字符串
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		dbUser, dbPassword, dbHost, dbPort, dbName)

	// 连接数据库
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return QueryDBOutput{
			Success: false,
			Error:   fmt.Sprintf("连接数据库失败: %v", err),
		}, nil
	}
	defer db.Close()

	// 设置连接超时
	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 执行查询
	rows, err := db.QueryContext(ctxWithTimeout, input.SQL)
	if err != nil {
		return QueryDBOutput{
			Success: false,
			Error:   fmt.Sprintf("执行查询失败: %v", err),
		}, nil
	}
	defer rows.Close()

	// 获取列名
	columns, err := rows.Columns()
	if err != nil {
		return QueryDBOutput{
			Success: false,
			Error:   fmt.Sprintf("获取列名失败: %v", err),
		}, nil
	}

	// 读取结果
	var result []map[string]interface{}
	for rows.Next() {
		// 创建值切片和指针切片
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// 扫描行数据
		if err := rows.Scan(valuePtrs...); err != nil {
			return QueryDBOutput{
				Success: false,
				Error:   fmt.Sprintf("扫描行数据失败: %v", err),
			}, nil
		}

		// 构建行数据map
		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// 处理 []byte 类型（MySQL返回的JSON等类型）
			if b, ok := val.([]byte); ok {
				// 尝试解析为JSON
				var jsonVal interface{}
				if err := json.Unmarshal(b, &jsonVal); err == nil {
					row[col] = jsonVal
				} else {
					row[col] = string(b)
				}
			} else {
				row[col] = val
			}
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return QueryDBOutput{
			Success: false,
			Error:   fmt.Sprintf("遍历结果集失败: %v", err),
		}, nil
	}

	fmt.Println("--> 模型调用了query_db工具" + time.Now().Format("2006-01-02 15:04:05"))
	return QueryDBOutput{
		Rows:     result,
		RowCount: len(result),
		Success:  true,
	}, nil
}

// QueryDBInput 定义数据库查询工具的输入参数
type QueryDBInput struct {
	SQL string `json:"sql"` // SQL SELECT 查询语句（只允许SELECT查询）
}

// QueryDBOutput 定义数据库查询工具的输出结果
type QueryDBOutput struct {
	Rows     []map[string]interface{} `json:"rows"`            // 查询结果行
	RowCount int                      `json:"row_count"`       // 行数
	Success  bool                     `json:"success"`         // 是否成功
	Error    string                   `json:"error,omitempty"` // 错误信息（如果有）
}
