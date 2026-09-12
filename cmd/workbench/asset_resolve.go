package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/ekk1/mygo/utils/assetstore"
)

const maxInlineAssetBytes int64 = 32 << 20

// resolveAssets opens only favorite assets and adds them to the native request.
// The returned cleanup owns only the new asset handles; existing uploads remain
// owned by the caller. All unrelated native parameters retain their JSON values.
func (a *app) resolveAssets(vendor, operation string, params json.RawMessage, uploads resourceUploads, ids map[string][]string) (json.RawMessage, resourceUploads, func(), error) {
	opened := []io.Closer{}
	cleanup := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	fail := func(err error) (json.RawMessage, resourceUploads, func(), error) {
		cleanup()
		return nil, nil, func() {}, err
	}
	count := 0
	for field, items := range ids {
		if field != "attachment" && field != "image" && field != "mask" && field != "file" {
			return fail(fmt.Errorf("unsupported asset field %q", field))
		}
		count += len(items)
	}
	if count == 0 {
		return params, uploads, cleanup, nil
	}
	if count > 20 {
		return fail(fmt.Errorf("at most 20 assets may be selected"))
	}
	if a.assets == nil {
		return fail(fmt.Errorf("asset library is unavailable"))
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil || body == nil {
		return fail(fmt.Errorf("native parameters must be an object"))
	}
	resolvedUploads := make(resourceUploads, len(uploads)+len(ids))
	for key, values := range uploads {
		resolvedUploads[key] = append([]resourceUpload(nil), values...)
	}
	var inlineBytes int64
	for _, field := range []string{"attachment", "image", "mask", "file"} {
		selected := ids[field]
		if (field == "mask" || field == "file") && len(selected) > 1 {
			return fail(fmt.Errorf("%s accepts one asset", field))
		}
		if vendor == "xai" && operation == "images.edit" && field == "image" && len(selected) > 0 && (body["image"] != nil || body["images"] != nil) {
			return fail(fmt.Errorf("image is already set in native parameters"))
		}
		for _, id := range selected {
			item, file, err := a.assets.OpenContent(id)
			if err != nil {
				return fail(err)
			}
			opened = append(opened, file)
			if !item.Favorite {
				return fail(fmt.Errorf("asset %q is no longer a favorite", item.Name))
			}
			typ, _, err := mime.ParseMediaType(item.ContentType)
			if err != nil {
				return fail(fmt.Errorf("invalid asset content type"))
			}
			image := strings.HasPrefix(typ, "image/") && safeAssetInline(typ)
			if (field == "attachment" || field == "image" || field == "mask") && !image {
				return fail(fmt.Errorf("%s requires an image asset", field))
			}
			if field == "mask" && typ != "image/png" {
				return fail(fmt.Errorf("mask requires a PNG asset"))
			}
			multipart := (field == "image" || field == "mask") && operation == "images.edit" && (vendor == "openai" || vendor == "compatible")
			if field == "file" {
				multipart = operation == "files.upload" || operation == "containers.files.upload" || ((operation == "audio.transcribe" || operation == "audio.translate") && vendor != "gemini")
				if !multipart && !(vendor == "gemini" && operation == "content.generate") {
					return fail(fmt.Errorf("file assets are not supported for %s", operation))
				}
			}
			if multipart {
				if (field == "mask" || field == "file") && len(resolvedUploads[field]) != 0 {
					return fail(fmt.Errorf("%s already has an upload", field))
				}
				resolvedUploads[field] = append(resolvedUploads[field], resourceUpload{Filename: item.Name, ContentType: item.ContentType, Reader: file})
				continue
			}
			inlineBytes += item.Size
			if inlineBytes > maxInlineAssetBytes {
				return fail(fmt.Errorf("inline assets exceed 32 MiB; use the provider file upload API"))
			}
			data, readErr := io.ReadAll(io.LimitReader(file, maxInlineAssetBytes+1))
			if readErr != nil {
				return fail(readErr)
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			url := "data:" + typ + ";base64," + encoded
			switch {
			case field == "attachment" && (vendor == "openai" || vendor == "compatible" || vendor == "xai") && operation == "responses.create":
				err = appendAssetInput(body, "input", "content", "input_text", map[string]any{"type": "input_image", "image_url": url})
			case field == "attachment" && (vendor == "openai" || vendor == "compatible" || vendor == "xai") && operation == "chat.create":
				err = appendAssetInput(body, "messages", "content", "text", map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			case field == "attachment" && vendor == "anthropic" && operation == "messages.create":
				err = appendAssetInput(body, "messages", "content", "text", map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": typ, "data": encoded}})
			case vendor == "gemini" && (operation == "content.generate" || operation == "images.generate") && field != "mask":
				err = appendAssetInput(body, "contents", "parts", "", map[string]any{"inlineData": map[string]any{"mimeType": typ, "data": encoded}})
			case vendor == "xai" && operation == "images.edit" && field == "image":
				image := map[string]any{"url": url, "type": "image_url"}
				if len(selected) == 1 {
					body["image"] = image
				} else {
					images, _ := body["images"].([]any)
					body["images"] = append(images, image)
				}
			default:
				err = fmt.Errorf("%s assets are not supported for %s/%s", field, vendor, operation)
			}
			if err != nil {
				return fail(err)
			}
		}
	}
	resolved, err := json.Marshal(body)
	if err != nil {
		return fail(err)
	}
	return resolved, resolvedUploads, cleanup, nil
}

func appendAssetInput(body map[string]any, arrayKey, partsKey, textType string, part any) error {
	var items []any
	switch current := body[arrayKey].(type) {
	case nil:
	case string:
		if arrayKey != "input" {
			return fmt.Errorf("%s must be an array", arrayKey)
		}
		items = []any{map[string]any{"role": "user", partsKey: current}}
	case []any:
		items = current
	default:
		return fmt.Errorf("%s must be an array", arrayKey)
	}
	var entry map[string]any
	if len(items) > 0 {
		last, _ := items[len(items)-1].(map[string]any)
		if last["role"] == "user" || arrayKey == "contents" && last["role"] == nil {
			entry = last
		}
	}
	if entry == nil {
		entry = map[string]any{"role": "user"}
		items = append(items, entry)
	}
	parts := []any{}
	switch content := entry[partsKey].(type) {
	case nil:
	case string:
		text := map[string]any{"text": content}
		if textType != "" {
			text["type"] = textType
		}
		parts = append(parts, text)
	case []any:
		parts = content
	default:
		return fmt.Errorf("%s must be text or an array", partsKey)
	}
	entry[partsKey] = append(parts, part)
	body[arrayKey] = items
	return nil
}

// captureUploads persists fresh multipart uploads and rewinds their seekable
// readers to their original positions so preview/send continue to use exact bytes.
func (a *app) captureUploads(p provider, operation, taskID, sessionID string, uploads resourceUploads) ([]string, error) {
	ids := []string{}
	if a.assets == nil {
		return ids, nil
	}
	for _, values := range uploads {
		for _, value := range values {
			reader, ok := value.Reader.(io.ReadSeeker)
			if !ok {
				return ids, fmt.Errorf("uploaded asset reader must be seekable")
			}
			position, err := reader.Seek(0, io.SeekCurrent)
			if err != nil {
				return ids, err
			}
			item, addErr := a.assets.Add(value.Filename, value.ContentType, assetstore.Source{"kind": "upload", "provider_id": p.ID, "operation": operation, "task_id": taskID, "session_id": sessionID}, reader, 0)
			_, seekErr := reader.Seek(position, io.SeekStart)
			if addErr != nil {
				return ids, addErr
			}
			ids = append(ids, item.ID)
			if seekErr != nil {
				return ids, seekErr
			}
		}
	}
	return ids, nil
}
