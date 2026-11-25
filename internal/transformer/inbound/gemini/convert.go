package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/bestruirui/octopus/internal/utils/xurl"
)

func trimSpace(value string) string {
	return strings.TrimSpace(value)
}

func convertGeminiSystemInstruction(instruction *model.GeminiContent) *model.Message {
	if instruction == nil || len(instruction.Parts) == 0 {
		return nil
	}

	texts := make([]string, 0, len(instruction.Parts))
	for _, part := range instruction.Parts {
		if part != nil && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) == 0 {
		return nil
	}

	text := strings.Join(texts, "\n")
	return &model.Message{
		Role: "system",
		Content: model.MessageContent{
			Content: &text,
		},
	}
}

func convertGeminiContent(content *model.GeminiContent) ([]model.Message, error) {
	if content == nil || len(content.Parts) == 0 {
		return nil, nil
	}

	role := strings.ToLower(strings.TrimSpace(content.Role))
	if role == "" {
		role = "user"
	}

	if role == "model" {
		message := convertGeminiModelContent(content)
		if message == nil {
			return nil, nil
		}
		return []model.Message{*message}, nil
	}

	return convertGeminiUserContent(content), nil
}

func convertGeminiUserContent(content *model.GeminiContent) []model.Message {
	messages := make([]model.Message, 0, 1)
	parts := make([]model.MessageContentPart, 0, len(content.Parts))
	texts := make([]string, 0, len(content.Parts))
	hasStructured := false

	flush := func() {
		if len(parts) == 0 && len(texts) == 0 {
			return
		}
		messages = append(messages, buildGeminiUserMessage(texts, parts, hasStructured))
		parts = nil
		texts = nil
		hasStructured = false
	}

	for _, part := range content.Parts {
		if part == nil {
			continue
		}

		switch {
		case part.FunctionResponse != nil:
			flush()
			if message := convertGeminiFunctionResponse(part.FunctionResponse); message != nil {
				messages = append(messages, *message)
			}
		case part.Text != "":
			texts = append(texts, part.Text)
			text := part.Text
			parts = append(parts, model.MessageContentPart{Type: "text", Text: &text})
		case part.InlineData != nil:
			hasStructured = true
			parts = append(parts, geminiBlobContentPart(part.InlineData))
		case part.FileData != nil:
			hasStructured = true
			parts = append(parts, geminiFileDataContentPart(part.FileData))
		default:
			log.Warnf("gemini inbound: dropping unsupported user part (text=%t inlineData=%t functionCall=%t functionResponse=%t)",
				part.Text != "", part.InlineData != nil, part.FunctionCall != nil, part.FunctionResponse != nil)
		}
	}

	flush()
	return messages
}

func buildGeminiUserMessage(texts []string, parts []model.MessageContentPart, hasStructured bool) model.Message {
	if hasStructured {
		return model.Message{Role: "user", Content: model.MessageContent{MultipleContent: parts}}
	}
	text := strings.Join(texts, "")
	return model.Message{Role: "user", Content: model.MessageContent{Content: &text}}
}

func convertGeminiModelContent(content *model.GeminiContent) *model.Message {
	message := &model.Message{Role: "assistant"}
	parts := make([]model.MessageContentPart, 0, len(content.Parts))
	texts := make([]string, 0, len(content.Parts))
	toolCalls := make([]model.ToolCall, 0)
	reasoningBlocks := make([]model.ReasoningBlock, 0)
	var reasoningContent *string
	hasStructured := false

	for _, part := range content.Parts {
		if part == nil {
			continue
		}

		switch {
		case part.Thought:
			block := model.ReasoningBlock{
				Kind:      model.ReasoningBlockKindThinking,
				Index:     len(reasoningBlocks),
				Text:      part.Text,
				Signature: part.ThoughtSignature,
				Provider:  "gemini",
			}
			reasoningBlocks = append(reasoningBlocks, block)
			if part.Text != "" && reasoningContent == nil {
				text := part.Text
				reasoningContent = &text
			}
		case part.Text != "":
			texts = append(texts, part.Text)
			text := part.Text
			parts = append(parts, model.MessageContentPart{Type: "text", Text: &text})
			if part.ThoughtSignature != "" {
				reasoningBlocks = append(reasoningBlocks, model.ReasoningBlock{
					Kind:      model.ReasoningBlockKindSignature,
					Index:     len(reasoningBlocks),
					Signature: part.ThoughtSignature,
					Provider:  "gemini",
				})
			}
		case part.InlineData != nil:
			hasStructured = true
			parts = append(parts, geminiBlobContentPart(part.InlineData))
		case part.FileData != nil:
			hasStructured = true
			parts = append(parts, geminiFileDataContentPart(part.FileData))
		case part.FunctionCall != nil:
			id := strings.TrimSpace(part.FunctionCall.ID)
			if id == "" {
				id = geminiFallbackToolCallID(part.FunctionCall.Name, len(toolCalls))
			}
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]interface{}{}
			}
			argsJSON, err := json.Marshal(args)
			if err != nil {
				log.Warnf("gemini inbound: failed to marshal functionCall args for %s: %v", part.FunctionCall.Name, err)
				argsJSON = []byte("{}")
			}
			toolCall := model.ToolCall{
				Index:            len(toolCalls),
				ID:               id,
				Type:             "function",
				Function:         model.FunctionCall{Name: part.FunctionCall.Name, Arguments: string(argsJSON)},
				ThoughtSignature: part.ThoughtSignature,
			}
			if part.ThoughtSignature != "" {
				toolCall.ProviderExtensions = geminiThoughtSignatureProviderExtension(part.ThoughtSignature)
				reasoningBlocks = append(reasoningBlocks, model.ReasoningBlock{
					Kind:         model.ReasoningBlockKindSignature,
					Index:        len(reasoningBlocks),
					Signature:    part.ThoughtSignature,
					Provider:     "gemini",
					ToolCallID:   id,
					ToolCallName: part.FunctionCall.Name,
				})
			}
			toolCalls = append(toolCalls, toolCall)
		default:
			log.Warnf("gemini inbound: dropping unsupported model part (text=%t inlineData=%t fileData=%t functionCall=%t)",
				part.Text != "", part.InlineData != nil, part.FileData != nil, part.FunctionCall != nil)
		}
	}

	if hasStructured {
		message.Content = model.MessageContent{MultipleContent: parts}
	} else if len(texts) > 0 {
		text := strings.Join(texts, "")
		message.Content = model.MessageContent{Content: &text}
	}
	if reasoningContent != nil {
		message.ReasoningContent = reasoningContent
	}
	if len(reasoningBlocks) > 0 {
		message.ReasoningBlocks = reasoningBlocks
	}
	if len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
	}

	if len(message.Content.MultipleContent) == 0 &&
		message.Content.Content == nil &&
		message.ReasoningContent == nil &&
		len(message.ReasoningBlocks) == 0 &&
		len(message.ToolCalls) == 0 {
		return nil
	}
	return message
}

func convertGeminiFunctionResponse(response *model.GeminiFunctionResponse) *model.Message {
	if response == nil {
		return nil
	}

	id := strings.TrimSpace(response.ID)
	if id == "" {
		id = strings.TrimSpace(response.Name)
	}
	if id == "" {
		id = "call_function_response"
	}

	payload, err := json.Marshal(response.Response)
	if err != nil {
		log.Warnf("gemini inbound: failed to marshal functionResponse for %s: %v", response.Name, err)
		payload = []byte("{}")
	}
	content := string(payload)
	name := response.Name

	message := &model.Message{
		Role:       "tool",
		ToolCallID: &id,
		Content:    model.MessageContent{Content: &content},
	}
	if name != "" {
		message.ToolCallName = &name
	}
	return message
}

func geminiBlobContentPart(blob *model.GeminiBlob) model.MessageContentPart {
	dataURL := fmt.Sprintf("data:%s;base64,%s", blob.MimeType, blob.Data)
	mediaType := strings.ToLower(blob.MimeType)
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return model.MessageContentPart{Type: "image_url", ImageURL: &model.ImageURL{URL: dataURL}}
	case strings.HasPrefix(mediaType, "audio/"):
		return model.MessageContentPart{
			Type:  "input_audio",
			Audio: &model.Audio{Format: audioFormatFromMime(mediaType), Data: blob.Data},
		}
	default:
		return model.MessageContentPart{
			Type: "file",
			File: &model.File{FileData: dataURL},
		}
	}
}

func geminiFileDataContentPart(fileData *model.GeminiFileData) model.MessageContentPart {
	return model.MessageContentPart{
		Type: "file",
		File: &model.File{
			FileURL:  fileData.FileURI,
			Filename: fileData.FileURI,
		},
	}
}

func audioFormatFromMime(mediaType string) string {
	if strings.Contains(mediaType, "wav") {
		return "wav"
	}
	return "mp3"
}

func applyGeminiGenerationConfig(request *model.InternalLLMRequest, config *model.GeminiGenerationConfig) {
	if config == nil {
		return
	}

	if config.MaxOutputTokens > 0 {
		maxTokens := int64(config.MaxOutputTokens)
		request.MaxTokens = &maxTokens
	}
	if config.Temperature != nil {
		request.Temperature = config.Temperature
	}
	if config.TopP != nil {
		request.TopP = config.TopP
	}
	if config.TopK != nil {
		topK := int64(*config.TopK)
		request.TopK = &topK
	}
	if config.PresencePenalty != nil {
		request.PresencePenalty = config.PresencePenalty
	}
	if config.FrequencyPenalty != nil {
		request.FrequencyPenalty = config.FrequencyPenalty
	}
	if config.Seed != nil {
		request.Seed = config.Seed
	}
	if len(config.StopSequences) > 0 {
		request.Stop = &model.Stop{MultipleStop: append([]string(nil), config.StopSequences...)}
	}
	if config.CandidateCount > 1 {
		request.SetTransformerMetadataValue(model.TransformerMetadataGeminiCandidateCount, fmt.Sprintf("%d", config.CandidateCount))
	}
	if config.MediaResolution != "" {
		request.SetTransformerMetadataValue(model.TransformerMetadataGeminiMediaResolution, config.MediaResolution)
	}
	if len(config.ResponseModalities) > 0 {
		request.Modalities = append([]string(nil), config.ResponseModalities...)
	}
	if len(config.SpeechConfig) > 0 {
		request.GeminiSpeechConfig = append(json.RawMessage(nil), config.SpeechConfig...)
		request.SetGeminiExtensions(model.GeminiExtension{SpeechConfig: append(json.RawMessage(nil), config.SpeechConfig...)})
	}
	if config.ResponseLogprobs != nil {
		request.Logprobs = config.ResponseLogprobs
	}
	if config.Logprobs != nil {
		topLogprobs := int64(*config.Logprobs)
		request.TopLogprobs = &topLogprobs
	}

	applyGeminiResponseFormat(request, config)

	if config.ThinkingConfig != nil {
		if config.ThinkingConfig.ThinkingBudget != nil {
			budget := int64(*config.ThinkingConfig.ThinkingBudget)
			request.ReasoningBudget = &budget
		}
		if level := strings.TrimSpace(config.ThinkingConfig.ThinkingLevel); level != "" {
			request.ReasoningEffort = strings.ToLower(level)
		}
		if config.ThinkingConfig.IncludeThoughts && request.ReasoningBudget == nil && request.ReasoningEffort == "" {
			request.AdaptiveThinking = true
		}
	}
}

func applyGeminiResponseFormat(request *model.InternalLLMRequest, config *model.GeminiGenerationConfig) {
	if config.ResponseMimeType == "" && config.ResponseSchema == nil && len(config.ResponseJsonSchema) == 0 {
		return
	}

	responseFormat := &model.ResponseFormat{}
	mimeType := strings.ToLower(strings.TrimSpace(config.ResponseMimeType))

	if strings.Contains(mimeType, "json") {
		responseFormat.Type = "json_object"
		if config.ResponseSchema != nil {
			responseFormat.Type = "json_schema"
			responseFormat.Schema = geminiSchemaToInternalSchema(config.ResponseSchema)
			if raw, err := json.Marshal(config.ResponseSchema); err == nil {
				responseFormat.RawSchema = raw
			}
		} else if len(config.ResponseJsonSchema) > 0 {
			responseFormat.Type = "json_schema"
			responseFormat.RawSchema = append(json.RawMessage(nil), config.ResponseJsonSchema...)
			if schema, err := model.ParseSchema(config.ResponseJsonSchema); err == nil {
				responseFormat.Schema = schema
			}
		}
	} else {
		responseFormat.Type = "text"
	}

	request.ResponseFormat = responseFormat
}

func geminiSchemaToInternalSchema(schema *model.GeminiSchema) *model.Schema {
	if schema == nil {
		return nil
	}

	out := &model.Schema{
		Type:             strings.ToLower(schema.Type),
		Format:           schema.Format,
		Description:      schema.Description,
		Nullable:         schema.Nullable,
		Required:         append([]string(nil), schema.Required...),
		Minimum:          schema.Minimum,
		Maximum:          schema.Maximum,
		MinItems:         schema.MinItems,
		MaxItems:         schema.MaxItems,
		PropertyOrdering: append([]string(nil), schema.PropertyOrdering...),
	}
	for _, enum := range schema.Enum {
		out.Enum = append(out.Enum, enum)
	}
	if len(schema.Properties) > 0 {
		out.Properties = make(map[string]*model.Schema, len(schema.Properties))
		for name, property := range schema.Properties {
			out.Properties[name] = geminiSchemaToInternalSchema(property)
		}
	}
	if schema.Items != nil {
		out.Items = geminiSchemaToInternalSchema(schema.Items)
	}
	for _, anyOf := range schema.AnyOf {
		out.AnyOf = append(out.AnyOf, geminiSchemaToInternalSchema(anyOf))
	}
	return out
}

func convertGeminiTools(tools []*model.GeminiTool) []model.Tool {
	if len(tools) == 0 {
		return nil
	}

	result := make([]model.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		for _, declaration := range tool.FunctionDeclarations {
			if declaration == nil {
				continue
			}
			parameters := declaration.ParametersJsonSchema
			if parameters == nil {
				parameters = declaration.Parameters
			}
			if parameters == nil {
				parameters = map[string]interface{}{}
			}
			raw, err := json.Marshal(parameters)
			if err != nil {
				log.Warnf("gemini inbound: failed to marshal function declaration parameters for %s: %v", declaration.Name, err)
				raw = []byte("{}")
			}
			result = append(result, model.Tool{
				Type: "function",
				Function: model.Function{
					Name:        declaration.Name,
					Description: declaration.Description,
					Parameters:  raw,
				},
			})
		}
		switch {
		case tool.GoogleSearch != nil:
			result = append(result, model.Tool{Type: "server_search"})
		case tool.CodeExecution != nil:
			result = append(result, model.Tool{Type: "code_execution"})
		case tool.UrlContext != nil:
			result = append(result, model.Tool{Type: "url_context"})
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func convertGeminiToolConfig(config *model.GeminiToolConfig) *model.ToolChoice {
	if config == nil || config.FunctionCallingConfig == nil {
		return nil
	}

	functionConfig := config.FunctionCallingConfig
	mode := strings.ToUpper(strings.TrimSpace(functionConfig.Mode))
	switch mode {
	case "AUTO":
		return &model.ToolChoice{ToolChoice: stringPointer("auto")}
	case "NONE":
		return &model.ToolChoice{ToolChoice: stringPointer("none")}
	case "ANY":
		if len(functionConfig.AllowedFunctionNames) > 0 {
			name := functionConfig.AllowedFunctionNames[0]
			return &model.ToolChoice{NamedToolChoice: &model.NamedToolChoice{
				Type:     "function",
				Function: &model.ToolFunction{Name: name},
				Name:     &name,
			}}
		}
		return &model.ToolChoice{ToolChoice: stringPointer("required")}
	default:
		return nil
	}
}

func convertInternalToGeminiResponse(response *model.InternalLLMResponse, isStream bool) *model.GeminiGenerateContentResponse {
	result := &model.GeminiGenerateContentResponse{}
	if response == nil {
		return result
	}

	result.ResponseId = response.ID
	result.ModelVersion = response.Model
	if response.Created > 0 {
		result.CreateTime = time.Unix(response.Created, 0).UTC().Format(time.RFC3339Nano)
	}

	for _, choice := range response.Choices {
		candidate := &model.GeminiCandidate{Index: choice.Index}
		if choice.FinishReason != nil {
			reason := model.ParseFinishReason(*choice.FinishReason).ToGemini()
			if reason != "" {
				candidate.FinishReason = &reason
			}
		}

		message := choice.Message
		if isStream {
			message = choice.Delta
		}
		if parts := convertInternalMessageToGeminiParts(message); len(parts) > 0 {
			candidate.Content = &model.GeminiContent{Role: "model", Parts: parts}
		}

		if candidate.Content == nil && candidate.FinishReason == nil {
			continue
		}
		result.Candidates = append(result.Candidates, candidate)
	}

	if response.Usage != nil {
		result.UsageMetadata = convertInternalUsageToGemini(response.Usage)
	}
	return result
}

func convertInternalMessageToGeminiParts(message *model.Message) []*model.GeminiPart {
	if message == nil {
		return nil
	}

	parts := make([]*model.GeminiPart, 0, len(message.Content.MultipleContent)+len(message.ToolCalls)+1)
	textPartIndexes := make([]int, 0, len(message.Content.MultipleContent)+1)
	signatureByToolID := map[string]string{}
	signatureByToolName := map[string]string{}
	looseSignatures := make([]string, 0)
	hasThinkingText := false

	for _, block := range message.ReasoningBlocks {
		switch block.Kind {
		case model.ReasoningBlockKindThinking:
			parts = append(parts, &model.GeminiPart{
				Text:             block.Text,
				Thought:          true,
				ThoughtSignature: block.Signature,
			})
			if block.Text != "" {
				hasThinkingText = true
			}
		case model.ReasoningBlockKindSignature:
			switch {
			case block.ToolCallID != "":
				signatureByToolID[block.ToolCallID] = block.Signature
			case block.ToolCallName != "":
				signatureByToolName[block.ToolCallName] = block.Signature
			case block.Signature != "":
				looseSignatures = append(looseSignatures, block.Signature)
			}
		}
	}

	if !hasThinkingText && message.ReasoningContent != nil && *message.ReasoningContent != "" {
		parts = append(parts, &model.GeminiPart{Text: *message.ReasoningContent, Thought: true})
	}

	if message.Content.Content != nil && *message.Content.Content != "" {
		textPartIndexes = append(textPartIndexes, len(parts))
		parts = append(parts, &model.GeminiPart{Text: *message.Content.Content})
	}
	for _, contentPart := range message.Content.MultipleContent {
		if part := internalContentPartToGeminiPart(contentPart); part != nil {
			if part.Text != "" && !part.Thought {
				textPartIndexes = append(textPartIndexes, len(parts))
			}
			parts = append(parts, part)
		}
	}
	for _, image := range message.Images {
		if part := internalContentPartToGeminiPart(image); part != nil {
			parts = append(parts, part)
		}
	}
	if message.Audio != nil && message.Audio.Data != "" {
		parts = append(parts, &model.GeminiPart{InlineData: &model.GeminiBlob{
			MimeType: "audio/mpeg",
			Data:     message.Audio.Data,
		}})
	}

	for _, toolCall := range message.ToolCalls {
		signature := strings.TrimSpace(toolCall.ThoughtSignature)
		if signature == "" {
			signature = signatureByToolID[toolCall.ID]
		}
		if signature == "" {
			signature = signatureByToolName[toolCall.Function.Name]
		}

		args := map[string]interface{}{}
		if strings.TrimSpace(toolCall.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
				log.Warnf("gemini inbound: failed to unmarshal tool call args for %s: %v", toolCall.Function.Name, err)
				args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
			}
		}

		part := &model.GeminiPart{
			FunctionCall: &model.GeminiFunctionCall{
				ID:   toolCall.ID,
				Name: toolCall.Function.Name,
				Args: args,
			},
		}
		if signature != "" {
			part.ThoughtSignature = signature
		}
		parts = append(parts, part)
	}

	if len(looseSignatures) > 0 {
		attachTo := -1
		if len(textPartIndexes) > 0 {
			attachTo = textPartIndexes[0]
		}
		if attachTo >= 0 && parts[attachTo] != nil && parts[attachTo].ThoughtSignature == "" {
			parts[attachTo].ThoughtSignature = looseSignatures[0]
		}
	}

	return parts
}

func internalContentPartToGeminiPart(part model.MessageContentPart) *model.GeminiPart {
	switch part.Type {
	case "text":
		if part.Text == nil || *part.Text == "" {
			return nil
		}
		return &model.GeminiPart{Text: *part.Text}
	case "image_url":
		if part.ImageURL == nil || part.ImageURL.URL == "" {
			return nil
		}
		if dataURL := xurl.ParseDataURL(part.ImageURL.URL); dataURL != nil {
			return &model.GeminiPart{InlineData: &model.GeminiBlob{MimeType: dataURL.MediaType, Data: dataURL.Data}}
		}
		return &model.GeminiPart{FileData: &model.GeminiFileData{MimeType: "image/*", FileURI: part.ImageURL.URL}}
	case "input_audio":
		if part.Audio == nil || part.Audio.Data == "" {
			return nil
		}
		return &model.GeminiPart{InlineData: &model.GeminiBlob{
			MimeType: audioMimeFromFormat(part.Audio.Format),
			Data:     part.Audio.Data,
		}}
	case "file":
		if part.File == nil {
			return nil
		}
		if dataURL := xurl.ParseDataURL(part.File.FileData); dataURL != nil {
			return &model.GeminiPart{InlineData: &model.GeminiBlob{MimeType: dataURL.MediaType, Data: dataURL.Data}}
		}
		if part.File.FileURL != "" {
			return &model.GeminiPart{FileData: &model.GeminiFileData{FileURI: part.File.FileURL}}
		}
		return nil
	default:
		return nil
	}
}

func convertInternalUsageToGemini(usage *model.Usage) *model.GeminiUsageMetadata {
	if usage == nil {
		return nil
	}

	result := &model.GeminiUsageMetadata{
		PromptTokenCount:        int(usage.PromptTokens),
		CandidatesTokenCount:    int(usage.CompletionTokens),
		TotalTokenCount:         int(usage.TotalTokens),
		ToolUsePromptTokenCount: int(usage.ToolUsePromptTokens),
	}
	if usage.PromptTokensDetails != nil {
		result.CachedContentTokenCount = int(usage.PromptTokensDetails.CachedTokens)
	}
	if usage.CompletionTokensDetails != nil {
		result.ThoughtsTokenCount = int(usage.CompletionTokensDetails.ReasoningTokens)
	}
	for _, detail := range usage.PromptModalityTokenDetails {
		result.PromptTokensDetails = append(result.PromptTokensDetails, model.GeminiModalityTokenCount{
			Modality:   detail.Modality,
			TokenCount: int(detail.TokenCount),
		})
	}
	for _, detail := range usage.CompletionModalityTokenDetails {
		result.CandidatesTokensDetails = append(result.CandidatesTokensDetails, model.GeminiModalityTokenCount{
			Modality:   detail.Modality,
			TokenCount: int(detail.TokenCount),
		})
	}
	return result
}

func marshalGeminiError(responseError *model.ResponseError) []byte {
	statusCode := responseError.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	message := responseError.Detail.Message
	if message == "" {
		message = responseError.Error()
	}
	status := strings.ToUpper(strings.ReplaceAll(http.StatusText(statusCode), " ", "_"))
	if status == "" {
		status = "UNKNOWN"
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    statusCode,
			"message": message,
			"status":  status,
		},
	})
	return payload
}

func geminiFallbackToolCallID(name string, index int) string {
	raw := fmt.Sprintf("call_%s_%d", name, index)
	var builder strings.Builder
	builder.Grow(len(raw))
	for _, char := range raw {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '_', char == '-':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	id := strings.Trim(builder.String(), "_-")
	if id == "" {
		return fmt.Sprintf("call_%d", index)
	}
	return id
}

func geminiThoughtSignatureProviderExtension(signature string) *model.ProviderExtensions {
	if signature == "" {
		return nil
	}
	return &model.ProviderExtensions{Gemini: &model.GeminiExtension{ThoughtSignature: signature}}
}

func stringPointer(value string) *string {
	return &value
}

func audioMimeFromFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "wav":
		return "audio/wav"
	case "mp3":
		return "audio/mpeg"
	default:
		return "audio/mpeg"
	}
}
