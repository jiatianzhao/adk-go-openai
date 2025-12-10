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

// Package openai implements the [model.LLM] interface for OpenAI-compatible APIs.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jiatianzhao/adk-go-openai/model"
	"google.golang.org/genai"
)

// ClientConfig holds configuration for the OpenAI client.
type ClientConfig struct {
	// APIKey is the API key for authentication.
	// If empty, will be read from environment variables based on the model name.
	APIKey string
	// BaseURL is the base URL for the API (e.g., "https://api.example.com/v1").
	// If empty, will be inferred from the model name.
	BaseURL string
	// HTTPClient is the HTTP client to use (optional)
	HTTPClient *http.Client
}

// openAIModel implements the model.LLM interface for OpenAI-compatible APIs.
type openAIModel struct {
	modelName  string
	config     *ClientConfig
	httpClient *http.Client
}

// NewModel returns [model.LLM], backed by an OpenAI-compatible API.
//
// It uses the provided context and configuration to initialize the HTTP client.
// The modelName specifies which model to target (e.g., "gpt-4", "gpt-4o-mini").
//
// If config is nil, it will be created with default values.
// If config.APIKey is empty, it will be read from OPENAI_API_KEY environment variable.
// If config.BaseURL is empty, it will be read from OPENAI_BASE_URL environment variable.
//
// An error is returned if no API key or base URL can be found.
func NewModel(ctx context.Context, modelName string, config *ClientConfig) (model.LLM, error) {
	// ctx is reserved for future use (e.g., client initialization with context)
	_ = ctx

	if config == nil {
		config = &ClientConfig{}
	}

	if config.APIKey == "" {
		config.APIKey = os.Getenv("OPENAI_API_KEY")
		if config.APIKey == "" {
			return nil, fmt.Errorf("openai: API key not found, set OPENAI_API_KEY environment variable or provide config.APIKey")
		}
	}

	if config.BaseURL == "" {
		config.BaseURL = os.Getenv("OPENAI_BASE_URL")
		if config.BaseURL == "" {
			return nil, fmt.Errorf("openai: base URL not found, set OPENAI_BASE_URL environment variable or provide config.BaseURL")
		}
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &openAIModel{
		modelName:  modelName,
		config:     config,
		httpClient: httpClient,
	}, nil
}

// Name returns the model name.
func (m *openAIModel) Name() string {
	return m.modelName
}

// GenerateContent calls the underlying OpenAI-compatible API.
func (m *openAIModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.maybeAppendUserContent(req)

	// Convert genai request to OpenAI format
	openaiReq, err := m.convertRequest(req)
	if err != nil {
		return func(yield func(*model.LLMResponse, error) bool) {
			yield(nil, fmt.Errorf("failed to convert request: %w", err))
		}
	}

	if stream {
		return m.generateStream(ctx, openaiReq)
	}
	return m.generate(ctx, openaiReq)
}

// OpenAI API types
type openAIRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Tools          []openAITool          `json:"tools,omitempty"`
	Temperature    *float64              `json:"temperature,omitempty"`
	MaxTokens      *int                  `json:"max_tokens,omitempty"`
	TopP           *float64              `json:"top_p,omitempty"`
	Stop           []string              `json:"stop,omitempty"`
	Stream         bool                  `json:"stream,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIResponseFormat struct {
	Type string `json:"type"` // "json_object" or "text"
}

type openAIMessage struct {
	Role             string           `json:"role"` // system, user, assistant, tool
	Content          any              `json:"content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ReasoningContent any              `json:"reasoning_content,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Index    *int               `json:"index,omitempty"`
	Type     string             `json:"type"` // "function"
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"` // "function"
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int            `json:"index"`
	Message      *openAIMessage `json:"message,omitempty"`
	Delta        *openAIMessage `json:"delta,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
}

type openAIUsage struct {
	PromptTokens        int                  `json:"prompt_tokens"`
	CompletionTokens    int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *promptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// convertRequest converts a model.LLMRequest to OpenAI format
func (m *openAIModel) convertRequest(req *model.LLMRequest) (*openAIRequest, error) {
	openaiReq := &openAIRequest{
		Model:    m.modelName,
		Messages: make([]openAIMessage, 0),
	}

	// Add system instruction if present
	if req.Config != nil && req.Config.SystemInstruction != nil {
		sysContent := extractTextFromContent(req.Config.SystemInstruction)
		if sysContent != "" {
			openaiReq.Messages = append(openaiReq.Messages, openAIMessage{
				Role:    "system",
				Content: sysContent,
			})
		}
	}

	// Convert contents to messages
	for _, content := range req.Contents {
		msgs, err := m.convertContent(content)
		if err != nil {
			return nil, fmt.Errorf("failed to convert content: %w", err)
		}
		openaiReq.Messages = append(openaiReq.Messages, msgs...)
	}

	// Convert tools
	if req.Config != nil && len(req.Config.Tools) > 0 {
		for _, tool := range req.Config.Tools {
			if tool.FunctionDeclarations != nil {
				for _, fn := range tool.FunctionDeclarations {
					openaiReq.Tools = append(openaiReq.Tools, convertFunctionDeclaration(fn))
				}
			}
		}
	}

	// Add generation config
	if req.Config != nil {
		if req.Config.Temperature != nil {
			temp := float64(*req.Config.Temperature)
			openaiReq.Temperature = &temp
		}
		if req.Config.MaxOutputTokens > 0 {
			maxTokens := int(req.Config.MaxOutputTokens)
			openaiReq.MaxTokens = &maxTokens
		}
		if req.Config.TopP != nil {
			topP := float64(*req.Config.TopP)
			openaiReq.TopP = &topP
		}
		if len(req.Config.StopSequences) > 0 {
			openaiReq.Stop = req.Config.StopSequences
		}
		if req.Config.ResponseMIMEType == "application/json" {
			openaiReq.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
		}
	}

	return openaiReq, nil
}

// convertContent converts genai.Content to OpenAI messages
func (m *openAIModel) convertContent(content *genai.Content) ([]openAIMessage, error) {
	if content == nil || len(content.Parts) == 0 {
		return nil, nil
	}

	role := content.Role
	if role == "model" {
		role = "assistant"
	}

	// Check if this is a tool response
	var toolMessages []openAIMessage
	for _, part := range content.Parts {
		if part.FunctionResponse != nil {
			responseJSON, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function response: %w", err)
			}
			toolCallID := part.FunctionResponse.ID
			if toolCallID == "" {
				toolCallID = "call_" + uuid.New().String()[:8]
			}
			toolMessages = append(toolMessages, openAIMessage{
				Role:       "tool",
				Content:    string(responseJSON),
				ToolCallID: toolCallID,
			})
		}
	}
	if len(toolMessages) > 0 {
		return toolMessages, nil
	}

	// Build message content
	var textParts []string
	var contentArray []map[string]any
	var toolCalls []openAIToolCall

	for _, part := range content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		} else if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			// Handle inline data (images, video, audio, files, etc.)
			mimeType := part.InlineData.MIMEType
			base64Data := base64.StdEncoding.EncodeToString(part.InlineData.Data)
			dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)

			if strings.HasPrefix(mimeType, "image/") {
				contentArray = append(contentArray, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": dataURI,
					},
				})
			} else if strings.HasPrefix(mimeType, "video/") {
				contentArray = append(contentArray, map[string]any{
					"type": "video_url",
					"video_url": map[string]any{
						"url": dataURI,
					},
				})
			} else if strings.HasPrefix(mimeType, "audio/") {
				contentArray = append(contentArray, map[string]any{
					"type": "audio_url",
					"audio_url": map[string]any{
						"url": dataURI,
					},
				})
			} else if mimeType == "application/pdf" || mimeType == "application/json" {
				contentArray = append(contentArray, map[string]any{
					"type": "file",
					"file": map[string]any{
						"file_data": dataURI,
					},
				})
			} else if strings.HasPrefix(mimeType, "text/") {
				textParts = append(textParts, string(part.InlineData.Data))
			}
		} else if part.FileData != nil && part.FileData.FileURI != "" {
			// Handle file data with URI
			contentArray = append(contentArray, map[string]any{
				"type": "file",
				"file": map[string]any{
					"file_id": part.FileData.FileURI,
				},
			})
		} else if part.FunctionCall != nil {
			argsJSON, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function args: %w", err)
			}
			callID := part.FunctionCall.ID
			if callID == "" {
				callID = "call_" + uuid.New().String()[:8]
			}
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   callID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	msg := openAIMessage{Role: role}

	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		if len(textParts) > 0 {
			msg.Content = strings.Join(textParts, "\n")
		}
	} else if len(contentArray) > 0 {
		// Add text parts to content array
		textMaps := make([]map[string]any, len(textParts))
		for i, text := range textParts {
			textMaps[i] = map[string]any{
				"type": "text",
				"text": text,
			}
		}
		msg.Content = append(textMaps, contentArray...)
	} else if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "\n")
	}

	return []openAIMessage{msg}, nil
}

// extractTextFromContent extracts and concatenates all text parts from a genai.Content.
func extractTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var texts []string
	for _, part := range content.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// convertFunctionDeclaration converts a genai.FunctionDeclaration to OpenAI tool format.
func convertFunctionDeclaration(fn *genai.FunctionDeclaration) openAITool {
	params := convertFunctionParameters(fn)

	return openAITool{
		Type: "function",
		Function: openAIFunction{
			Name:        fn.Name,
			Description: fn.Description,
			Parameters:  params,
		},
	}
}

// convertFunctionParameters extracts parameters from a FunctionDeclaration.
// It prefers ParametersJsonSchema (new standard) over Parameters (legacy).
func convertFunctionParameters(fn *genai.FunctionDeclaration) map[string]any {
	// Try ParametersJsonSchema first (new standard used by functiontool)
	if fn.ParametersJsonSchema != nil {
		if params := tryConvertJsonSchema(fn.ParametersJsonSchema); params != nil {
			return params
		}
	}

	// Fallback to Parameters (legacy format used by older code)
	if fn.Parameters != nil {
		return convertLegacyParameters(fn.Parameters)
	}

	return make(map[string]any)
}

// tryConvertJsonSchema attempts to convert ParametersJsonSchema to map[string]any.
// Returns nil if conversion fails.
func tryConvertJsonSchema(schema any) map[string]any {
	// Fast path: already a map
	if params, ok := schema.(map[string]any); ok {
		return params
	}

	// Slow path: convert via JSON marshaling (handles *jsonschema.Schema, etc.)
	jsonBytes, err := json.Marshal(schema)
	if err != nil {
		return nil
	}

	var params map[string]any
	if err := json.Unmarshal(jsonBytes, &params); err != nil {
		return nil
	}

	return params
}

// convertLegacyParameters converts genai.Schema to OpenAI parameters format.
func convertLegacyParameters(schema *genai.Schema) map[string]any {
	params := map[string]any{
		"type": "object",
	}

	if schema.Properties != nil {
		props := make(map[string]any)
		for k, v := range schema.Properties {
			props[k] = schemaToMap(v)
		}
		params["properties"] = props
	}

	if len(schema.Required) > 0 {
		params["required"] = schema.Required
	}

	return params
}

// schemaToMap recursively converts a genai.Schema to a map representation.
func schemaToMap(schema *genai.Schema) map[string]any {
	result := make(map[string]any)
	if schema.Type != genai.TypeUnspecified {
		result["type"] = strings.ToLower(string(schema.Type))
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if schema.Items != nil {
		result["items"] = schemaToMap(schema.Items)
	}
	if schema.Properties != nil {
		props := make(map[string]any)
		for k, v := range schema.Properties {
			props[k] = schemaToMap(v)
		}
		result["properties"] = props
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}
	return result
}

// generate performs a non-streaming API call
func (m *openAIModel) generate(ctx context.Context, openaiReq *openAIRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.doRequest(ctx, openaiReq)
		if err != nil {
			yield(nil, err)
			return
		}

		llmResp, err := m.convertResponse(resp)
		if err != nil {
			yield(nil, err)
			return
		}
		yield(llmResp, nil)
	}
}

// generateStream performs a streaming API call
func (m *openAIModel) generateStream(ctx context.Context, openaiReq *openAIRequest) iter.Seq2[*model.LLMResponse, error] {
	openaiReq.Stream = true

	return func(yield func(*model.LLMResponse, error) bool) {
		httpResp, err := m.sendRequest(ctx, openaiReq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer httpResp.Body.Close()

		scanner := bufio.NewScanner(httpResp.Body)
		var textBuffer strings.Builder
		var toolCalls []openAIToolCall
		var usage *openAIUsage
		var debugFile *os.File
		var chunkID string

		// 流式过滤器,用于实时检测和过滤错误格式
		streamFilter := newStreamFilter()

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
				// 即使解析失败，如果已有文件句柄，也保存原始 data
				if debugFile != nil {
					fmt.Fprintln(debugFile, data)
				}
				continue
			}

			// 保存调试数据到文件
			if chunk.ID != "" {
				// 第一次获取到 ID 时打开文件
				if debugFile == nil {
					chunkID = chunk.ID
					debugDir := "/usr/local/bin/chats"
					if err := os.MkdirAll(debugDir, 0755); err == nil {
						debugFilePath := filepath.Join(debugDir, chunkID)
						if f, err := os.OpenFile(debugFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
							debugFile = f
						}
					}
				}
				// 追加写入 data，每次换行
				if debugFile != nil {
					fmt.Fprintln(debugFile, fmt.Sprintf("%#v", chunk.Choices))
				}
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta
			if delta == nil {
				continue
			}

			// Handle text content
			if delta.Content != nil {
				if text, ok := delta.Content.(string); ok && text != "" {
					// 使用流式过滤器处理文本内容
					filteredText, parsedToolCalls := streamFilter.processText(text)

					// 如果解析出了工具调用,添加到工具调用列表
					if len(parsedToolCalls) > 0 {
						toolCalls = append(toolCalls, parsedToolCalls...)
					}

					// 只有过滤后的文本才写入缓冲区和输出
					if filteredText != "" {
						textBuffer.WriteString(filteredText)
						// Yield partial response
						llmResp := &model.LLMResponse{
							Content: &genai.Content{
								Role: "model",
								Parts: []*genai.Part{
									{Text: filteredText},
								},
							},
							Partial: true,
						}
						if !yield(llmResp, nil) {
							return
						}
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
					// Ensure we have enough space in toolCalls slice
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

			// Handle usage
			if chunk.Usage != nil {
				usage = chunk.Usage
			}

			// Handle finish
			if choice.FinishReason != "" {
				// 处理流式过滤器缓冲区中剩余的内容
				finalFilteredText, finalParsedToolCalls := streamFilter.flush()
				if finalFilteredText != "" {
					textBuffer.WriteString(finalFilteredText)
				}
				if len(finalParsedToolCalls) > 0 {
					toolCalls = append(toolCalls, finalParsedToolCalls...)
				}

				text := textBuffer.String()
				finishReason := choice.FinishReason
				// Fallback: 检测并修复错误的工具调用格式(兼容旧逻辑)
				if len(toolCalls) == 0 && hasMalformedToolCallPattern(text) {
					parsedCalls, cleanedText := parseMalformedToolCalls(text)
					if len(parsedCalls) > 0 {
						toolCalls = parsedCalls
						textBuffer.Reset()
						textBuffer.WriteString(cleanedText)
						// 如果解析出了工具调用,应该将 finish_reason 改为 tool_calls
						if finishReason == "stop" {
							finishReason = "tool_calls"
						}
					}
				} else if len(toolCalls) > 0 && finishReason == "stop" {
					// 如果流式过滤器解析出了工具调用,应该将 finish_reason 改为 tool_calls
					finishReason = "tool_calls"
				}
				finalResp := m.buildFinalResponse(textBuffer.String(), toolCalls, usage, finishReason)
				yield(finalResp, nil)
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if debugFile != nil {
				debugFile.Close()
			}
			yield(nil, fmt.Errorf("stream error: %w", err))
			return
		}

		// 关闭调试文件
		if debugFile != nil {
			debugFile.Close()
		}

		// Fallback: if stream ended without FinishReason but we have accumulated content,
		// send the final response. This handles non-compliant OpenAI-compatible APIs.
		// 处理流式过滤器缓冲区中剩余的内容
		finalFilteredText, finalParsedToolCalls := streamFilter.flush()
		if finalFilteredText != "" {
			textBuffer.WriteString(finalFilteredText)
		}
		if len(finalParsedToolCalls) > 0 {
			toolCalls = append(toolCalls, finalParsedToolCalls...)
		}

		if textBuffer.Len() > 0 || len(toolCalls) > 0 {
			text := textBuffer.String()
			// Fallback: 检测并修复错误的工具调用格式(兼容旧逻辑)
			if len(toolCalls) == 0 && hasMalformedToolCallPattern(text) {
				parsedCalls, cleanedText := parseMalformedToolCalls(text)
				if len(parsedCalls) > 0 {
					toolCalls = parsedCalls
					textBuffer.Reset()
					textBuffer.WriteString(cleanedText)
				}
			}
			finishReason := "stop"
			if len(toolCalls) > 0 {
				finishReason = "tool_calls"
			}
			finalResp := m.buildFinalResponse(textBuffer.String(), toolCalls, usage, finishReason)
			yield(finalResp, nil)
		}
	}
}

// sendRequest creates and sends an HTTP request to the OpenAI API.
// Caller is responsible for closing the response body.
func (m *openAIModel) sendRequest(ctx context.Context, openaiReq *openAIRequest) (*http.Response, error) {
	reqBody, err := json.Marshal(openaiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := strings.TrimSuffix(m.config.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.config.APIKey)

	httpResp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, fmt.Errorf("API error (status %d): %s", httpResp.StatusCode, string(body))
	}

	return httpResp, nil
}

// doRequest performs the HTTP request to the OpenAI API
func (m *openAIModel) doRequest(ctx context.Context, openaiReq *openAIRequest) (*openAIResponse, error) {
	httpResp, err := m.sendRequest(ctx, openaiReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var resp openAIResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// convertResponse converts OpenAI response to model.LLMResponse
func (m *openAIModel) convertResponse(resp *openAIResponse) (*model.LLMResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := resp.Choices[0]
	msg := choice.Message
	if msg == nil {
		return nil, fmt.Errorf("no message in choice")
	}

	var parts []*genai.Part

	// Handle reasoning content (thought process) - prepend before regular content
	if reasoningParts := extractReasoningParts(msg.ReasoningContent); len(reasoningParts) > 0 {
		parts = append(parts, reasoningParts...)
	}

	// Get tool calls - either from structured response or parsed from text
	toolCalls := msg.ToolCalls
	textContent := ""
	if msg.Content != nil {
		if text, ok := msg.Content.(string); ok {
			textContent = text
		}
	}

	// If no structured tool calls, try parsing from text content
	if len(toolCalls) == 0 && textContent != "" {
		parsedCalls, remainder := parseToolCallsFromText(textContent)
		if len(parsedCalls) > 0 {
			toolCalls = parsedCalls
			textContent = remainder
		}
	}

	// Handle text content
	if textContent != "" {
		parts = append(parts, genai.NewPartFromText(textContent))
	}

	// Handle tool calls
	for _, tc := range toolCalls {
		if tc.ID == "" && tc.Function.Name == "" && tc.Function.Arguments == "" {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tool arguments: %w", err)
		}
		part := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		part.FunctionCall.ID = tc.ID
		parts = append(parts, part)
	}

	llmResp := &model.LLMResponse{
		Content: &genai.Content{
			Role:  "model",
			Parts: parts,
		},
	}

	// Add usage metadata
	llmResp.UsageMetadata = buildUsageMetadata(resp.Usage)

	// Map finish reason
	llmResp.FinishReason = mapFinishReason(choice.FinishReason)

	return llmResp, nil
}

func (m *openAIModel) buildFinalResponse(text string, toolCalls []openAIToolCall, usage *openAIUsage, finishReason string) *model.LLMResponse {
	var parts []*genai.Part

	if text != "" {
		parts = append(parts, genai.NewPartFromText(text))
	}

	for _, tc := range toolCalls {
		if tc.ID == "" && tc.Function.Name == "" && tc.Function.Arguments == "" {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			continue
		}
		part := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		part.FunctionCall.ID = tc.ID
		parts = append(parts, part)
	}

	llmResp := &model.LLMResponse{
		Content: &genai.Content{
			Role:  "model",
			Parts: parts,
		},
		FinishReason:  mapFinishReason(finishReason),
		UsageMetadata: buildUsageMetadata(usage),
	}

	return llmResp
}

// buildUsageMetadata converts OpenAI usage data to genai usage metadata.
func buildUsageMetadata(usage *openAIUsage) *genai.GenerateContentResponseUsageMetadata {
	if usage == nil {
		return nil
	}
	metadata := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(usage.PromptTokens),
		CandidatesTokenCount: int32(usage.CompletionTokens),
		TotalTokenCount:      int32(usage.TotalTokens),
	}
	// Add cached token count if available
	if usage.PromptTokensDetails != nil {
		metadata.CachedContentTokenCount = int32(usage.PromptTokensDetails.CachedTokens)
	}
	return metadata
}

// extractReasoningParts extracts reasoning/thought content from provider-specific payloads.
// It converts various reasoning formats (string, list, map) into genai.Part with Thought=true.
func extractReasoningParts(reasoningContent any) []*genai.Part {
	if reasoningContent == nil {
		return nil
	}

	var parts []*genai.Part
	extractTexts(reasoningContent, &parts)
	return parts
}

// extractTexts recursively extracts text from reasoning content and creates thought parts.
func extractTexts(value any, parts *[]*genai.Part) {
	if value == nil {
		return
	}

	switch v := value.(type) {
	case string:
		if v != "" {
			*parts = append(*parts, &genai.Part{Text: v, Thought: true})
		}
	case []any:
		for _, item := range v {
			extractTexts(item, parts)
		}
	case map[string]any:
		// LiteLLM/OpenAI nests reasoning text under known keys
		for _, key := range []string{"text", "content", "reasoning", "reasoning_content"} {
			if text, ok := v[key].(string); ok && text != "" {
				*parts = append(*parts, &genai.Part{Text: text, Thought: true})
			}
		}
	}
}

// parseToolCallsFromText extracts inline JSON tool calls from text responses.
// Some models embed tool calls as JSON objects in their text output.
// Returns the extracted tool calls and any remaining text.
func parseToolCallsFromText(text string) ([]openAIToolCall, string) {
	if text == "" {
		return nil, ""
	}

	var toolCalls []openAIToolCall
	var remainder strings.Builder
	cursor := 0

	for cursor < len(text) {
		braceIndex := strings.Index(text[cursor:], "{")
		if braceIndex == -1 {
			remainder.WriteString(text[cursor:])
			break
		}
		braceIndex += cursor

		remainder.WriteString(text[cursor:braceIndex])

		// Try to parse JSON starting at brace
		var candidate map[string]any
		decoder := json.NewDecoder(strings.NewReader(text[braceIndex:]))
		if err := decoder.Decode(&candidate); err != nil {
			remainder.WriteString(text[braceIndex : braceIndex+1])
			cursor = braceIndex + 1
			continue
		}

		// Calculate end position
		endPos := braceIndex + int(decoder.InputOffset())

		// Check if this looks like a tool call
		name, hasName := candidate["name"].(string)
		args, hasArgs := candidate["arguments"]
		if hasName && hasArgs {
			argsStr := ""
			switch a := args.(type) {
			case string:
				argsStr = a
			default:
				if jsonBytes, err := json.Marshal(args); err == nil {
					argsStr = string(jsonBytes)
				}
			}

			callID := "call_" + uuid.New().String()[:8]
			if id, ok := candidate["id"].(string); ok && id != "" {
				callID = id
			}

			toolCalls = append(toolCalls, openAIToolCall{
				ID:   callID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      name,
					Arguments: argsStr,
				},
			})
		} else {
			remainder.WriteString(text[braceIndex:endPos])
		}
		cursor = endPos
	}

	return toolCalls, strings.TrimSpace(remainder.String())
}

// mapFinishReason maps OpenAI finish_reason strings to genai.FinishReason values.
// Note: tool_calls and function_call map to STOP because tool calls represent
// normal completion where the model stopped to invoke tools.
func mapFinishReason(reason string) genai.FinishReason {
	switch reason {
	case "stop":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "tool_calls", "function_call":
		return genai.FinishReasonStop
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

// hasMalformedToolCallPattern 检测文本中是否包含错误的工具调用格式
// 当模型输出 [{'type': 'text', 'text': ' 后跟 <|tool_call_begin|> 等标签时,表示格式错误
func hasMalformedToolCallPattern(text string) bool {
	// 检测是否包含错误的文本格式模式
	hasTextPattern := strings.Contains(text, "[{'type': 'text'") ||
		strings.Contains(text, "[{\"type\": \"text\"") ||
		strings.Contains(text, "['type': 'text'") ||
		strings.Contains(text, "[\"type\": \"text\"")

	// 检测是否包含工具调用标签
	hasToolCallTags := strings.Contains(text, "<|tool_call_begin|>") ||
		strings.Contains(text, "<|tool_call_end|>") ||
		strings.Contains(text, "<|tool_call_argument_begin|>") ||
		strings.Contains(text, "<|tool_calls_section_end|>")

	return hasTextPattern && hasToolCallTags
}

// parseMalformedToolCalls 从包含错误格式的文本中解析出工具调用
// 解析格式: <|tool_call_begin|>functions.function_name:index<|tool_call_argument_begin|>{...}<|tool_call_end|>
func parseMalformedToolCalls(text string) ([]openAIToolCall, string) {
	var toolCalls []openAIToolCall
	var cleanedParts []string

	// 移除开头的错误文本格式模式(如 [{'type': 'text', 'text': '...'}] )
	text = removeMalformedTextPattern(text)

	// 使用正则表达式或字符串匹配来解析工具调用
	// 格式: <|tool_call_begin|>functions.function_name:index<|tool_call_argument_begin|>{...}<|tool_call_end|>
	remaining := text
	startTag := "<|tool_call_begin|>"
	endTag := "<|tool_call_end|>"
	argBeginTag := "<|tool_call_argument_begin|>"

	for {
		beginIdx := strings.Index(remaining, startTag)
		if beginIdx == -1 {
			// 没有更多工具调用,添加剩余文本
			if len(remaining) > 0 {
				cleanedParts = append(cleanedParts, remaining)
			}
			break
		}

		// 添加工具调用之前的内容
		if beginIdx > 0 {
			beforeText := strings.TrimSpace(remaining[:beginIdx])
			if beforeText != "" && !strings.HasPrefix(beforeText, "[{") && !strings.HasPrefix(beforeText, "]}") {
				cleanedParts = append(cleanedParts, beforeText)
			}
		}

		// 提取工具调用部分
		callStart := beginIdx + len(startTag)
		endIdx := strings.Index(remaining[callStart:], endTag)
		if endIdx == -1 {
			// 没有找到结束标签,跳出
			cleanedParts = append(cleanedParts, remaining[beginIdx:])
			break
		}

		callEnd := callStart + endIdx
		callContent := remaining[callStart:callEnd]

		// 解析函数名和参数
		argBeginIdx := strings.Index(callContent, argBeginTag)
		if argBeginIdx == -1 {
			// 没有参数开始标签,跳过
			remaining = remaining[callEnd+len(endTag):]
			continue
		}

		// 提取函数名部分 (functions.function_name:index)
		functionNamePart := strings.TrimSpace(callContent[:argBeginIdx])
		functionName := ""
		if strings.HasPrefix(functionNamePart, "functions.") {
			// 移除 functions. 前缀
			namePart := strings.TrimPrefix(functionNamePart, "functions.")
			// 提取函数名(可能包含 :index)
			if colonIdx := strings.Index(namePart, ":"); colonIdx != -1 {
				functionName = namePart[:colonIdx]
			} else {
				functionName = namePart
			}
		}

		// 提取参数部分
		argStart := argBeginIdx + len(argBeginTag)
		argsStr := strings.TrimSpace(callContent[argStart:])

		// 验证参数是否为有效的 JSON
		var args map[string]any
		if err := json.Unmarshal([]byte(argsStr), &args); err == nil && functionName != "" {
			// 成功解析,创建工具调用
			callID := "call_" + uuid.New().String()[:8]
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   callID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      functionName,
					Arguments: argsStr,
				},
			})
		}

		// 继续处理剩余部分
		remaining = remaining[callEnd+len(endTag):]

		// 移除可能的 <|tool_calls_section_end|> 标签
		if strings.HasPrefix(remaining, "<|tool_calls_section_end|>") {
			remaining = strings.TrimPrefix(remaining, "<|tool_calls_section_end|>")
		}
	}

	cleanedText := strings.Join(cleanedParts, "\n")
	return toolCalls, cleanedText
}

// removeMalformedTextPattern 移除错误的文本格式模式
// 例如: [{'type': 'text', 'text': '...'}] 或 [{"type": "text", "text": "..."}]
func removeMalformedTextPattern(text string) string {
	result := text

	// 处理单引号格式: [{'type': 'text', 'text': '...'}]
	for {
		startIdx := strings.Index(result, `[{'type': 'text', 'text': '`)
		if startIdx == -1 {
			startIdx = strings.Index(result, `['type': 'text', 'text': '`)
		}
		if startIdx == -1 {
			break
		}

		// 找到文本开始位置
		textStart := startIdx
		var textEnd int = -1

		// 从文本开始位置查找结束位置
		// 需要找到匹配的 '}] 或 ']
		searchStart := textStart + len(`[{'type': 'text', 'text': '`)
		if searchStart > len(result) {
			searchStart = textStart + len(`['type': 'text', 'text': '`)
		}

		// 查找结束模式,需要处理转义的单引号
		for i := searchStart; i < len(result); i++ {
			if result[i] == '\'' && i+1 < len(result) {
				if result[i+1] == '}' && i+2 < len(result) && result[i+2] == ']' {
					textEnd = i + 3
					break
				}
				if result[i+1] == ']' {
					textEnd = i + 2
					break
				}
			}
		}

		if textEnd > 0 {
			result = result[:textStart] + result[textEnd:]
		} else {
			break
		}
	}

	// 处理双引号格式: [{"type": "text", "text": "..."}]
	for {
		startIdx := strings.Index(result, `[{"type": "text", "text": "`)
		if startIdx == -1 {
			startIdx = strings.Index(result, `["type": "text", "text": "`)
		}
		if startIdx == -1 {
			break
		}

		// 找到文本开始位置
		textStart := startIdx
		var textEnd int = -1

		// 从文本开始位置查找结束位置
		// 需要找到匹配的 "}] 或 "]
		searchStart := textStart + len(`[{"type": "text", "text": "`)
		if searchStart > len(result) {
			searchStart = textStart + len(`["type": "text", "text": "`)
		}

		// 查找结束模式,需要处理转义的双引号
		for i := searchStart; i < len(result); i++ {
			if result[i] == '"' && i > 0 && result[i-1] != '\\' {
				if i+1 < len(result) && result[i+1] == '}' && i+2 < len(result) && result[i+2] == ']' {
					textEnd = i + 3
					break
				}
				if i+1 < len(result) && result[i+1] == ']' {
					textEnd = i + 2
					break
				}
			}
		}

		if textEnd > 0 {
			result = result[:textStart] + result[textEnd:]
		} else {
			break
		}
	}

	return result
}

// maybeAppendUserContent appends a user content, so that model can continue to output.
func (m *openAIModel) maybeAppendUserContent(req *model.LLMRequest) {
	if len(req.Contents) == 0 {
		req.Contents = append(req.Contents, genai.NewContentFromText("Handle the requests as specified in the System Instruction.", "user"))
		return
	}

	if last := req.Contents[len(req.Contents)-1]; last != nil && last.Role != "user" {
		req.Contents = append(req.Contents, genai.NewContentFromText("Continue processing previous requests as instructed. Exit or provide a summary if no more outputs are needed.", "user"))
	}
}

// streamFilter 流式过滤器,用于实时检测和过滤错误格式
type streamFilter struct {
	// 累积缓冲区,用于跨chunk检测
	buffer strings.Builder
	// 解析出的工具调用
	parsedToolCalls []openAIToolCall
}

const malformedPatternLen = 27 // len(`[{'type': 'text', 'text': '`)

func newStreamFilter() *streamFilter {
	return &streamFilter{
		parsedToolCalls: make([]openAIToolCall, 0),
	}
}

// flush 处理缓冲区中剩余的内容,返回所有过滤后的文本和工具调用
func (sf *streamFilter) flush() (string, []openAIToolCall) {
	if sf.buffer.Len() == 0 {
		return "", sf.parsedToolCalls[:0]
	}

	fullText := sf.buffer.String()
	sf.parsedToolCalls = sf.parsedToolCalls[:0]

	// 处理工具调用标签
	fullText, toolCalls := sf.processToolCallTags(fullText)
	sf.parsedToolCalls = append(sf.parsedToolCalls, toolCalls...)

	// 处理错误格式
	fullText = sf.processMalformedText(fullText)

	sf.buffer.Reset()
	return fullText, sf.parsedToolCalls
}

// processText 处理流式文本,返回过滤后的文本和解析出的工具调用
func (sf *streamFilter) processText(text string) (string, []openAIToolCall) {
	sf.buffer.WriteString(text)
	fullText := sf.buffer.String()
	sf.parsedToolCalls = sf.parsedToolCalls[:0]

	// 先处理工具调用标签
	fullText, toolCalls := sf.processToolCallTags(fullText)
	sf.parsedToolCalls = append(sf.parsedToolCalls, toolCalls...)

	// 再处理错误格式
	fullText = sf.processMalformedText(fullText)

	// 检查是否有未完成的模式需要保留在缓冲区
	// 保留最后500个字符用于跨chunk检测(足够检测工具调用标签和错误格式)
	keepLen := 500
	if len(fullText) <= keepLen {
		// 内容太少,可能还有未完成的模式,全部保留
		sf.buffer.Reset()
		sf.buffer.WriteString(fullText)
		return "", sf.parsedToolCalls
	}

	// 检查末尾是否有未完成的模式
	// 如果末尾包含工具调用标签的开始或错误格式的开始,需要保留更多
	needsKeep := false
	suffix := fullText[len(fullText)-keepLen:]
	if strings.Contains(suffix, "<|tool_call") ||
		strings.Contains(suffix, "[{'type': 'text'") ||
		strings.Contains(suffix, `[{"type": "text"`) {
		// 找到最后一个完整模式的位置
		lastToolCallEnd := strings.LastIndex(fullText[:len(fullText)-keepLen], "<|tool_call_end|>")
		lastMalformedEnd := strings.LastIndex(fullText[:len(fullText)-keepLen], "'}]")
		if lastMalformedEnd == -1 {
			lastMalformedEnd = strings.LastIndex(fullText[:len(fullText)-keepLen], `"}]`)
		}

		// 如果末尾有未完成的模式,需要保留
		if lastToolCallEnd < len(fullText)-keepLen-50 || lastMalformedEnd < len(fullText)-keepLen-50 {
			needsKeep = true
		}
	}

	if needsKeep {
		// 保留更多内容
		keepLen = 500
		if len(fullText) <= keepLen {
			sf.buffer.Reset()
			sf.buffer.WriteString(fullText)
			return "", sf.parsedToolCalls
		}
	}

	// 输出可以安全输出的部分
	output := fullText[:len(fullText)-keepLen]
	sf.buffer.Reset()
	sf.buffer.WriteString(fullText[len(fullText)-keepLen:])

	return output, sf.parsedToolCalls
}

// processMalformedText 处理错误文本格式
func (sf *streamFilter) processMalformedText(text string) string {
	result := text

	// 查找并移除 [{'type': 'text', 'text': '...'}]
	for {
		idx := strings.Index(result, "[{'type': 'text', 'text': '")
		if idx == -1 {
			idx = strings.Index(result, `[{"type": "text", "text": "`)
		}
		if idx == -1 {
			break
		}

		// 找到对应的结束位置 '}] 或 "}]
		searchStart := idx + malformedPatternLen
		endIdx := -1

		// 查找 '}] 模式
		for i := searchStart; i < len(result)-1; i++ {
			if result[i] == '}' && result[i+1] == ']' {
				endIdx = i - 1
				break
			}
		}

		if endIdx > 0 {
			// 移除整个错误模式
			result = result[searchStart:endIdx]
		} else {
			// 还没接收完,保留(但这种情况应该很少,因为模式已经完整了)
			break
		}
	}

	return result
}

// processToolCallTags 处理工具调用标签
func (sf *streamFilter) processToolCallTags(text string) (string, []openAIToolCall) {
	var toolCalls []openAIToolCall
	result := text

	for {
		beginIdx := strings.Index(result, "<|tool_call_begin|>")
		if beginIdx == -1 {
			break
		}

		endIdx := strings.Index(result[beginIdx:], "<|tool_call_end|>")
		if endIdx == -1 {
			// 还没接收完
			break
		}
		endIdx += beginIdx

		// 提取工具调用内容
		callContent := result[beginIdx+len("<|tool_call_begin|>") : endIdx]
		tc := sf.parseToolCall(callContent)
		if tc != nil {
			toolCalls = append(toolCalls, *tc)
		}

		// 移除工具调用部分
		result = result[:beginIdx] + result[endIdx+len("<|tool_call_end|>"):]

		index := strings.Index(result, "<|tool_call_end|>")
		if index > 0 {
			result = result[:index]
		}
	}

	// 移除其他标签
	//result = strings.ReplaceAll(result, "<|tool_call_argument_begin|>", "")
	result = strings.ReplaceAll(result, "<|tool_calls_section_end|>", "")

	return result, toolCalls
}

// parseToolCall 解析工具调用内容
func (sf *streamFilter) parseToolCall(content string) *openAIToolCall {
	argBeginIdx := strings.Index(content, "<|tool_call_argument_begin|>")
	if argBeginIdx == -1 {
		return nil
	}

	functionPart := strings.TrimSpace(content[:argBeginIdx])
	argsPart := strings.TrimSpace(content[argBeginIdx+len("<|tool_call_argument_begin|>"):])

	if !strings.HasPrefix(functionPart, "functions.") {
		return nil
	}

	namePart := strings.TrimPrefix(functionPart, "functions.")
	functionName := namePart
	if colonIdx := strings.Index(namePart, ":"); colonIdx != -1 {
		functionName = namePart[:colonIdx]
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsPart), &args); err != nil {
		return nil
	}

	return &openAIToolCall{
		ID:   "call_" + uuid.New().String()[:8],
		Type: "function",
		Function: openAIFunctionCall{
			Name:      functionName,
			Arguments: argsPart,
		},
	}
}
