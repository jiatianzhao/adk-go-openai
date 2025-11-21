# 根因分析 Agent 示例

这个示例演示了如何使用 adk-go 的 openai 模型对接 KimiK2，构建一个带工具的 agent 来进行根因分析。

## 功能说明

1. **对接 KimiK2**: 使用 OpenAI 兼容的 API 接口对接 KimiK2 模型
2. **文件读取工具**: 封装了一个读取本地文件的工具，用于读取 trace 数据
3. **Agent 构建**: 创建一个专业的根因分析 agent
4. **工具调用**: Agent 可以自动调用工具读取文件数据
5. **根因分析**: 基于 trace 数据和异常信息输出根因分析结论

## 使用步骤

### 1. 配置环境变量

```bash
# KimiK2 API 配置（测试阶段使用官方 API）
export KIMIK2_BASE_URL="https://api.moonshot.cn/v1"
export KIMIK2_API_KEY="your_api_key_here"
export KIMIK2_MODEL="moonshot-v1-8k"

# 数据目录配置（可选，默认为 ./data）
export DATA_DIR="./data"
```

### 2. 准备数据文件

将你的 trace 数据文件放在 `data/` 目录下。示例中已经包含了一个示例文件：
- `data/payment_service_logs.txt` - 支付服务日志示例

### 3. 运行示例

```bash
cd examples/root_cause_analysis
go run main.go
```

## 代码结构说明

### main.go

主要包含以下部分：

1. **readFileTool**: 文件读取工具函数
   - 输入：文件路径（相对于数据目录）
   - 输出：文件内容
   - 包含安全检查，防止路径遍历攻击

2. **模型配置**: 
   - 使用 `openai.NewModel` 创建 OpenAI 兼容的模型适配器
   - 支持自定义 BaseURL 和 APIKey
   - 可配置超时、重试等参数

3. **Agent 创建**:
   - 使用 `llmagent.New` 创建 LLM Agent
   - 配置专业的根因分析指令
   - 注册文件读取工具

4. **Runner 执行**:
   - 创建 Session 和 Runner
   - 调用 `runner.Run` 执行 Agent
   - 处理响应事件并输出结果

## 完整链路流程

```
用户输入 (trace 数据描述)
    ↓
Agent 接收请求
    ↓
Agent 决定调用工具 (read_trace_file)
    ↓
工具执行 (读取文件)
    ↓
工具返回结果给 Agent
    ↓
Agent 基于文件内容和用户输入进行分析
    ↓
Agent 输出根因分析结论
```

## 自定义扩展

### 添加更多工具

你可以添加更多工具来增强 Agent 的能力，例如：

- 数据库查询工具
- 日志搜索工具
- 指标查询工具
- 依赖关系分析工具

示例：

```go
// 添加数据库查询工具
dbQueryTool, err := functiontool.New(
    functiontool.Config{
        Name:        "query_database",
        Description: "查询数据库获取 trace 相关信息",
    },
    queryDatabase,
)

// 在 Agent 中注册
Tools: []tool.Tool{
    readFileToolInstance,
    dbQueryTool,
}
```

### 修改 Agent 指令

你可以根据实际需求修改 Agent 的 `Instruction`，使其更适合你的根因分析场景。

## 注意事项

1. **API Key**: 请确保设置正确的 KIMIK2_API_KEY，否则请求会失败
2. **数据目录**: 确保数据目录存在且包含必要的文件
3. **文件路径**: 工具只允许访问数据目录内的文件，确保安全
4. **模型选择**: 根据你的 KimiK2 部署选择合适的模型名称

## 生产环境部署

在生产环境中：

1. 将 `KIMIK2_BASE_URL` 设置为你自己部署的 KimiK2 服务地址
2. 使用环境变量或密钥管理服务来管理 API Key
3. 根据需要调整超时时间和重试策略
4. 添加日志和监控
5. 考虑添加错误处理和重试机制

