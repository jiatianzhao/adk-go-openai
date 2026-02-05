package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"testing"

	"github.com/jiatianzhao/adk-go-openai/model"
	"google.golang.org/genai"
)

// parseStreamFromReader：把你 generateStream 里「拿到 httpResp.Body 之后」的解析逻辑抽出来，便于直接喂文件。
// 结构体/方法（openAIResponse/openAIToolCall/openAIUsage/buildFinalResponse 等）全部复用你现有代码。
func (m *openAIModel) parseStreamFromReader(r io.Reader) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		scanner := bufio.NewScanner(r)
		// 防止单行过长被 Scanner 截断（默认 64K）
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

		var textBuffer strings.Builder
		var reasoningBuffer strings.Builder
		var toolCalls []openAIToolCall
		var usage *openAIUsage

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk openAIResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			// Handle usage（注意：你原逻辑是先处理 usage，再判断 choices）
			if chunk.Usage != nil {
				usage = chunk.Usage
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta
			if delta == nil {
				continue
			}

			// Handle reasoning content
			if delta.ReasoningContent != nil {
				if text, ok := delta.ReasoningContent.(string); ok && text != "" {
					reasoningBuffer.WriteString(text)
					llmResp := &model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{Text: text, Thought: true},
							},
						},
						Partial: true,
					}
					if !yield(llmResp, nil) {
						return
					}
				}
			}

			// Handle text content
			if delta.Content != nil {
				if text, ok := delta.Content.(string); ok && text != "" {
					textBuffer.WriteString(text)
					llmResp := &model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{Text: text},
							},
						},
						Partial: true,
					}
					if !yield(llmResp, nil) {
						return
					}
				}
			}

			// Handle tool calls
			if len(delta.ToolCalls) > 0 {
				for idx, tc := range delta.ToolCalls {
					targetIdx := idx
					if tc.Index != nil {
						targetIdx = *tc.Index
					}
					for len(toolCalls) <= targetIdx {
						toolCalls = append(toolCalls, openAIToolCall{})
					}
					if tc.ID != "" {
						toolCalls[targetIdx].ID = tc.ID
					}
					if tc.Type != "" {
						toolCalls[targetIdx].Type = tc.Type
					}
					if tc.Function.Name != "" {
						toolCalls[targetIdx].Function.Name += tc.Function.Name
					}
					toolCalls[targetIdx].Function.Arguments += tc.Function.Arguments
				}
			}

			// Handle finish
			if choice.FinishReason != "" {
				finalResp := m.buildFinalResponse(
					textBuffer.String(),
					reasoningBuffer.String(),
					toolCalls,
					usage,
					choice.FinishReason,
				)
				yield(finalResp, nil)
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, fmt.Errorf("stream error: %w", err))
			return
		}
	}
}

func Test_StreamYield_FromFile(t *testing.T) {
	f, err := os.Open("./res.txt")
	if err != nil {
		t.Fatalf("open ./res.txt failed: %v", err)
	}
	defer f.Close()

	ctx := context.Background()
	_ = ctx // 你如果后面想扩展也方便

	m := &openAIModel{} // 直接用你现有实现；buildFinalResponse 等都会走你项目里的逻辑

	seq := m.parseStreamFromReader(f)

	i := 0
	for resp, err := range seq {
		i++
		if err != nil {
			fmt.Printf("[yield #%d] ERROR: %v\n", i, err)
			continue
		}
		fmt.Printf("[yield #%d]\n", i)
		dumpLLMResponse(resp)
	}
}

func dumpLLMResponse(resp *model.LLMResponse) {
	if resp == nil {
		fmt.Println("RESP: <nil>")
		return
	}

	// 1) 先打印你最关心的：Content.Role + Parts
	if resp.Content == nil {
		fmt.Printf("RESP(Content=nil) Partial=%v FinishReason=%q ErrorCode=%q ErrorMessage=%q\n",
			resp.Partial, resp.FinishReason, resp.ErrorCode, resp.ErrorMessage)
	} else {
		fmt.Printf("RESP Role=%q Partial=%v FinishReason=%q ErrorCode=%q ErrorMessage=%q\n",
			resp.Content.Role, resp.Partial, resp.FinishReason, resp.ErrorCode, resp.ErrorMessage)

		for i, p := range resp.Content.Parts {
			if p == nil {
				fmt.Printf("  Part[%d]=<nil>\n", i)
				continue
			}
			fmt.Printf("  Part[%d] Text=%q Thought=%v\n", i, p.Text, p.Thought)
			// 如果 Part 里还有其它你关心字段（如 ToolCall/InlineData 等），你也可以继续加打印
		}
	}

	// 2) 再打印整坨 JSON（对比最方便）
	// 注意：如果里面有 interface{} 或不可导出字段，Marshal 可能会失败；失败就退回 fmt 递归
	if b, err := json.MarshalIndent(resp, "", "  "); err == nil {
		fmt.Println("RESP JSON:")
		fmt.Println(string(b))
	} else {
		fmt.Printf("RESP JSON marshal failed: %v\n", err)
		// 退化：至少把 Content/Parts 已经打印了
	}
}
