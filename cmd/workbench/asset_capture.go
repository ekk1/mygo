package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/assetstore"
	"github.com/ekk1/mygo/utils/httpclient"
)

// captureAssets stores generated media without modifying the native response.
// Binary download temporary files remain owned by the caller. Capture failures
// return already committed IDs so completed media need not be discarded.
func (a *app) captureAssets(ctx context.Context, p provider, operation, taskID, sessionID string, result any) ([]string, error) {
	return a.captureAssetResult(ctx, p, operation, taskID, sessionID, result, true)
}

func (a *app) captureAssetResult(ctx context.Context, p provider, operation, taskID, sessionID string, result any, network bool) ([]string, error) {
	ids := []string{}
	if a.assets == nil || result == nil {
		return ids, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	source := assetstore.Source{"kind": "generated", "provider_id": p.ID, "operation": operation, "task_id": taskID, "session_id": sessionID}
	add := func(name, typ string, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		asset, err := a.assets.Add(name, typ, source, &assetContextReader{ctx: ctx, reader: r}, 0)
		if err == nil {
			ids = append(ids, asset.ID)
		}
		return err
	}
	if download, ok := result.(resourceDownload); ok {
		file, err := os.Open(download.Path)
		if err != nil {
			return ids, err
		}
		defer file.Close()
		err = add(download.Name, download.ContentType, file)
		return ids, err
	}
	// Decoding a separate tree also covers RawMessage and typed streaming results.
	raw, err := json.Marshal(result)
	if err != nil {
		return ids, err
	}
	var tree any
	if err = json.Unmarshal(raw, &tree); err != nil {
		return ids, err
	}
	seen := map[[32]byte]bool{}
	var failures []error
	inline := func(encoded, typ, name string) {
		if encoded == "" {
			return
		}
		if strings.HasPrefix(encoded, "data:") {
			comma := strings.IndexByte(encoded, ',')
			if comma < 0 || !strings.HasSuffix(encoded[:comma], ";base64") {
				failures = append(failures, fmt.Errorf("unsupported generated data URL"))
				return
			}
			typ = strings.TrimSuffix(strings.TrimPrefix(encoded[:comma], "data:"), ";base64")
			encoded = encoded[comma+1:]
		}
		digest := sha256.Sum256([]byte(typ + "\x00" + encoded))
		if seen[digest] {
			return
		}
		seen[digest] = true
		if int64(len(encoded)) > assetstore.DefaultMaxBytes*4/3+8 {
			failures = append(failures, assetstore.ErrTooLarge)
			return
		}
		var reader io.Reader = base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
		parsed, params, _ := mime.ParseMediaType(typ)
		if strings.EqualFold(parsed, "audio/L16") || strings.EqualFold(parsed, "audio/pcm") {
			rate, _ := strconv.Atoi(params["rate"])
			if rate == 0 {
				rate = 24000
			}
			channels, _ := strconv.Atoi(params["channels"])
			if channels == 0 {
				channels = 1
			}
			if rate < 1 || rate > 192000 || channels < 1 || channels > 8 {
				failures = append(failures, fmt.Errorf("invalid PCM audio format"))
				return
			}
			size := base64.StdEncoding.DecodedLen(len(encoded)) - (len(encoded) - len(strings.TrimRight(encoded, "=")))
			reader = io.MultiReader(bytes.NewReader(assetWAVHeader(size, rate, channels)), reader)
			typ, name = "audio/wav", "speech.wav"
		}
		buffered := bufio.NewReader(reader)
		prefix, peekErr := buffered.Peek(512)
		if peekErr != nil && peekErr != io.EOF {
			failures = append(failures, peekErr)
			return
		}
		detected := http.DetectContentType(prefix)
		if detected == "application/ogg" {
			if strings.HasPrefix(typ, "audio/") {
				detected = "audio/ogg"
			} else if strings.HasPrefix(typ, "video/") {
				detected = "video/ogg"
			}
		}
		if safeAssetInline(detected) {
			typ, name = detected, assetMediaName(detected)
		}
		reader = buffered
		if err := add(name, typ, reader); err != nil {
			failures = append(failures, err)
		}
	}
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, child := range value {
				walk(child)
			}
		case map[string]any:
			if encoded, _ := value["b64_json"].(string); encoded != "" {
				inline(encoded, "image/png", "image.png")
			}
			if value["type"] == "image_generation_call" {
				if encoded, _ := value["result"].(string); encoded != "" {
					inline(encoded, "image/png", "image.png")
				}
			}
			for _, key := range []string{"inlineData", "inline_data"} {
				if content, ok := value[key].(map[string]any); ok {
					typ := assetString(content, "mimeType", "mime_type")
					if typ == "" {
						typ = "application/octet-stream"
					}
					inline(assetString(content, "data"), typ, assetMediaName(typ))
				}
			}
			if audio, ok := value["audio"].(map[string]any); ok {
				inline(assetString(audio, "data"), "audio/wav", "speech.wav")
			}
			if strings.HasPrefix(operation, "audio.") {
				inline(assetString(value, "audio_base64"), "audio/mpeg", "speech.mp3")
			}
			for key, child := range value {
				if key != "inlineData" && key != "inline_data" && key != "audio" && key != "native_events" && key != "HTTP" && key != "http" {
					walk(child)
				}
			}
		}
	}
	walk(tree)
	// Only provider-defined image/video result fields trigger downloads. Text,
	// tool arguments, citations and arbitrary nested URLs are never fetched.
	root, _ := tree.(map[string]any)
	urls := []string{}
	if strings.HasPrefix(operation, "images.") {
		if data, ok := root["data"].([]any); ok {
			for _, value := range data {
				item, _ := value.(map[string]any)
				if uri := assetString(item, "url"); uri != "" {
					urls = append(urls, uri)
				}
			}
		}
	}
	if strings.HasPrefix(operation, "videos.") {
		if video, ok := root["video"].(map[string]any); ok {
			if uri := assetString(video, "url", "uri"); uri != "" {
				urls = append(urls, uri)
			}
		}
		if response, ok := root["response"].(map[string]any); ok {
			if generated, ok := response["generateVideoResponse"].(map[string]any); ok {
				response = generated
			}
			for _, key := range []string{"generatedSamples", "generatedVideos", "generated_videos"} {
				if videos, ok := response[key].([]any); ok {
					for _, value := range videos {
						item, _ := value.(map[string]any)
						video, _ := item["video"].(map[string]any)
						if uri := assetString(video, "uri", "url"); uri != "" {
							urls = append(urls, uri)
						}
					}
				}
			}
		}
	}
	seenURL := map[string]bool{}
	if network {
		for _, uri := range urls {
			if seenURL[uri] {
				continue
			}
			seenURL[uri] = true
			if strings.HasPrefix(uri, "data:") {
				inline(uri, "application/octet-stream", "generated-media")
				continue
			}
			if err := a.captureAssetURL(ctx, p, uri, add); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return ids, errors.Join(failures...)
}

type assetContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *assetContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func assetString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := value[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func assetMediaName(typ string) string {
	parsed, _, _ := mime.ParseMediaType(typ)
	switch strings.ToLower(parsed) {
	case "image/jpeg":
		return "image.jpg"
	case "image/webp":
		return "image.webp"
	case "image/gif":
		return "image.gif"
	case "image/png":
		return "image.png"
	case "audio/mpeg":
		return "speech.mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "speech.wav"
	case "audio/ogg":
		return "speech.ogg"
	case "video/mp4":
		return "video.mp4"
	case "video/webm":
		return "video.webm"
	case "video/ogg":
		return "video.ogv"
	default:
		return "generated-media"
	}
}

func assetWAVHeader(size, rate, channels int) []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(size+36))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(size))
	return header
}

func (a *app) captureAssetURL(ctx context.Context, p provider, rawURL string, add func(string, string, io.Reader) error) error {
	uri, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid generated media URL")
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid provider URL")
	}
	sameOrigin := func(u *url.URL) bool {
		return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
	}
	if p.Kind == "gemini" && sameOrigin(uri) {
		// Gemini generated files require profile authentication. Reconstruct the
		// known native files endpoint; never attach credentials to the supplied URL.
		prefix := strings.TrimRight(base.Path, "/") + "/v1beta/files/"
		if strings.HasPrefix(uri.Path, prefix) && uri.User == nil {
			id := strings.TrimSuffix(strings.TrimPrefix(uri.Path, prefix), ":download")
			if id == "" || strings.ContainsAny(id, "/\\") {
				return fmt.Errorf("invalid generated file ID")
			}
			params, _ := json.Marshal(map[string]string{"name": "files/" + id, "filename": "video.mp4"})
			result, executeErr := a.executeNative(ctx, p, "files.download", params, nil, nil)
			if download, ok := result.(resourceDownload); ok {
				defer os.Remove(download.Path)
				if executeErr != nil {
					return executeErr
				}
				f, openErr := os.Open(download.Path)
				if openErr != nil {
					return openErr
				}
				defer f.Close()
				return add(download.Name, download.ContentType, f)
			}
			if executeErr != nil {
				return executeErr
			}
			return fmt.Errorf("generated file download returned no binary data")
		}
		return fmt.Errorf("unsupported Gemini generated file URL")
	}
	validate := func(u *url.URL) error {
		if u.User != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && sameOrigin(u))) {
			return fmt.Errorf("unsupported generated media URL")
		}
		return nil
	}
	if err = validate(uri); err != nil {
		return err
	}
	client, err := httpclient.New(httpclient.Config{ProxyURL: p.ProxyURL, Timeout: 2 * time.Minute})
	if err != nil {
		return err
	}
	client.Transport = &assetTransport{base: client.Transport.(*http.Transport), providerURL: base}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many media redirects")
		}
		return validate(req.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return fmt.Errorf("invalid generated media request")
	}
	// No profile auth headers are attached, including on same-origin CDN links.
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("generated media download failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("generated media download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > assetstore.DefaultMaxBytes {
		return assetstore.ErrTooLarge
	}
	name := path.Base(uri.Path)
	if name == "." || name == "/" || name == "" {
		name = assetMediaName(response.Header.Get("Content-Type"))
	}
	return add(name, response.Header.Get("Content-Type"), response.Body)
}
