package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// shellLiteral keeps arbitrary request content literal, including newlines and
// shell substitutions. Body data is passed through Bash's printf builtin so a
// long body never becomes an external program's command-line argument.
func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func nativeCurl(p provider, spec nativeRequestSpec) (string, error) {
	if strings.ContainsRune(string(spec.jsonBody), 0) {
		return "", fmt.Errorf("curl 导出不支持包含 NUL 的请求正文")
	}
	var setup strings.Builder
	setup.WriteString("# Bash 4+ 脚本；需要 curl。API_KEY 使用当前 profile 的凭据，保存的 key 不会导出。\n(\nset -euo pipefail\n: \"${API_KEY:?请先设置 API_KEY}\"\n")
	setup.WriteString("case \"$API_KEY\" in *$'\\r'*|*$'\\n'*) printf '%s\\n' 'API_KEY 不能含换行' >&2; exit 1;; esac\n")
	if p.ProxyURL != "" && p.ProxyURL != "-" {
		setup.WriteString("# PROXY_URL 填写当前 profile 的完整代理地址（包括所需认证）。\n: \"${PROXY_URL:?请先设置 PROXY_URL}\"\n")
	}
	if spec.geminiFile {
		return nativeGeminiCurl(p, spec, setup.String())
	}
	args := nativeCurlArgs(p, spec.preview.Method, shellLiteral(spec.preview.URL), spec.headers)
	if spec.preview.ContentType == "multipart/form-data" {
		args = append(args, "--form-escape")
		setup.WriteString("# multipart 需要 curl 7.81+（--form-escape 保留文件名中的引号与反斜杠）。\n")
		setup.WriteString("# FILE_1、FILE_2… 按以下文件说明设置绝对本地路径；使用与预览相同的文件。\n")
		setup.WriteString(`workbench_curl_escape() { local value=$1; value=${value//\\/\\\\}; value=${value//\"/\\\"}; printf '%s' "$value"; }
`)
		keys := make([]string, 0, len(spec.form))
		for key := range spec.form {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		// Keep the total inline text budget well below per-argument and process
		// argv limits. Larger fields stay text parts, but curl reads their bytes
		// from private temporary files rather than command-line arguments.
		inlineBytes, tempFields := 0, 0
		for _, key := range keys {
			if !nativeCurlField(key) {
				return "", fmt.Errorf("curl 导出不支持包含等号、NUL 或换行的 multipart 字段名")
			}
			for _, value := range spec.form[key] {
				if strings.ContainsRune(value, 0) {
					return "", fmt.Errorf("curl 导出不支持包含 NUL 的 multipart 文本字段")
				}
				if inlineBytes+len(key)+len(value) <= 32<<10 {
					args = append(args, "--form-string "+shellLiteral(key+"="+value))
					inlineBytes += len(key) + len(value)
					continue
				}
				if tempFields == 0 {
					setup.WriteString("# 长文本字段还需要 mktemp、rm；临时文本文件在脚本结束时删除。\nWORKBENCH_FORM_FILES=()\ntrap 'rm -f -- \"${WORKBENCH_FORM_FILES[@]-}\"' EXIT\n")
				}
				tempFields++
				variable := fmt.Sprintf("WORKBENCH_FORM_%d", tempFields)
				setup.WriteString("WORKBENCH_FORM_FILE=$(mktemp)\nWORKBENCH_FORM_FILES+=(\"$WORKBENCH_FORM_FILE\")\nprintf '%s' " + shellLiteral(value) + " > \"$WORKBENCH_FORM_FILE\"\n" + variable + "=$(workbench_curl_escape \"$WORKBENCH_FORM_FILE\")\n")
				args = append(args, "--form "+shellLiteral(key+"=<\"")+"\"$"+variable+"\""+shellLiteral("\""))
			}
		}
		// Preserve order within repeated file fields (image order is meaningful).
		// The display preview sorts filenames, whereas transport uses the upload slice.
		fileFields := make([]string, 0, len(spec.uploads))
		for field := range spec.uploads {
			fileFields = append(fileFields, field)
		}
		sort.Strings(fileFields)
		files := []nativeFilePreview{}
		for _, field := range fileFields {
			for _, upload := range spec.uploads[field] {
				files = append(files, nativeFilePreview{Field: field, Filename: upload.Filename, ContentType: upload.ContentType})
			}
		}
		for i, file := range files {
			if !nativeCurlField(file.Field) || strings.ContainsAny(file.Filename, "\r\n\x00") {
				return "", fmt.Errorf("curl 导出不支持此 multipart 字段名或包含 NUL/换行的文件名")
			}
			if file.ContentType == "" || !nativeCurlMIME(file.ContentType) {
				return "", fmt.Errorf("curl 导出要求文件 MIME 为不含参数的 type/subtype；此文件 MIME 无法无损导出")
			}
			variable := fmt.Sprintf("FILE_%d", i+1)
			setup.WriteString("# " + variable + " 文件名：" + strconv.Quote(file.Filename) + "，MIME：" + file.ContentType + "\n")
			setup.WriteString(nativeCurlFileSetup(variable))
			setup.WriteString("WORKBENCH_" + variable + "=$(workbench_curl_escape \"$" + variable + "\")\n")
			escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(file.Filename)
			form := shellLiteral(file.Field+"=@\"") + "\"$WORKBENCH_" + variable + "\"" + shellLiteral("\";filename=\""+escaped+"\";type="+file.ContentType)
			args = append(args, "--form "+form)
		}
	} else if len(spec.jsonBody) > 0 {
		args = append(args, "--header "+shellLiteral("Content-Type: "+spec.preview.ContentType), "--data-binary @-")
		setup.WriteString("printf '%s' " + shellLiteral(string(spec.jsonBody)) + " | \\\n")
	}
	setup.WriteString(strings.Join(args, " \\\n  ") + "\n)\n")
	return setup.String(), nil
}

func nativeCurlArgs(p provider, method, target string, headers http.Header) []string {
	args := []string{"curl --disable --globoff --path-as-is --silent --show-error --fail --max-time 600", "--request " + shellLiteral(method), "--url " + target}
	switch p.ProxyURL {
	case "": // Keep curl's environment-proxy behavior.
	case "-":
		args = append(args, "--noproxy '*'")
	default:
		args = append(args, "--proxy \"${PROXY_URL:?请先设置 PROXY_URL}\"", "--noproxy ''")
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			switch strings.ToLower(key) {
			case "authorization":
				args = append(args, "--header "+shellLiteral(key+": Bearer ")+"\"${API_KEY:?请先设置 API_KEY}\"")
			case "x-api-key", "x-goog-api-key":
				args = append(args, "--header "+shellLiteral(key+": ")+"\"${API_KEY:?请先设置 API_KEY}\"")
			default:
				args = append(args, "--header "+shellLiteral(key+": "+value))
			}
		}
	}
	return args
}

func nativeCurlField(field string) bool {
	return field != "" && !strings.ContainsAny(field, "=\r\n\x00")
}
func nativeCurlMIME(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$&^_.+-", r)) {
				return false
			}
		}
	}
	return true
}

func nativeCurlFileSetup(variable string) string {
	return ": \"${" + variable + ":?请设置绝对文件路径 " + variable + "}\"\ncase \"$" + variable + "\" in *$'\\r'*|*$'\\n'*) printf '%s\\n' '导出脚本不支持含换行的本地路径' >&2; exit 1;; /*) ;; *) printf '%s\\n' '文件路径必须为绝对路径' >&2; exit 1;; esac\n"
}

// Gemini returns an authenticated upload URL in its first response. Read only
// that header and constrain it to the same origin before attaching credentials.
func nativeGeminiCurl(p provider, spec nativeRequestSpec, setup string) (string, error) {
	if len(spec.preview.Files) != 1 || spec.preview.Files[0].Size < 0 {
		return "", fmt.Errorf("Gemini curl 上传导出需要一个大小已知的文件")
	}
	file := spec.preview.Files[0]
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.ContainsAny(contentType, "\r\n\x00") {
		return "", fmt.Errorf("curl 导出不支持包含换行或 NUL 的文件 MIME")
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return "", fmt.Errorf("Gemini curl 导出需要有效的 base URL")
	}
	var out strings.Builder
	out.WriteString(setup)
	out.WriteString("# Gemini 两阶段上传；还需要系统已有的 wc、mktemp、rm。FILE_1 必须与预览文件内容相同。\n")
	out.WriteString(nativeCurlFileSetup("FILE_1"))
	out.WriteString("WORKBENCH_FILE_SIZE=$(wc -c < \"$FILE_1\")\nif [ \"$WORKBENCH_FILE_SIZE\" -ne " + strconv.FormatInt(file.Size, 10) + " ]; then printf '%s\\n' '文件大小与预览不同，请重新预览' >&2; exit 1; fi\n")
	out.WriteString("WORKBENCH_HEADERS=$(mktemp)\ntrap 'rm -f -- \"$WORKBENCH_HEADERS\"' EXIT\n")
	headers := nativeGeminiStageHeaders(spec, false)
	args := nativeCurlArgs(p, http.MethodPost, shellLiteral(spec.preview.URL), headers)
	args = append(args, "--dump-header \"$WORKBENCH_HEADERS\"", "--data-binary @-")
	out.WriteString("printf '%s' " + shellLiteral(string(spec.jsonBody)) + " | \\\n" + strings.Join(args, " \\\n  ") + "\n")
	out.WriteString("WORKBENCH_UPLOAD_URL=''\nwhile IFS= read -r WORKBENCH_HEADER; do\n  WORKBENCH_HEADER=${WORKBENCH_HEADER%$'\\r'}\n  case \"${WORKBENCH_HEADER,,}\" in\n    http/*) WORKBENCH_UPLOAD_URL='' ;;\n    x-goog-upload-url:*) WORKBENCH_UPLOAD_URL=${WORKBENCH_HEADER#*:}; WORKBENCH_UPLOAD_URL=${WORKBENCH_UPLOAD_URL#\"${WORKBENCH_UPLOAD_URL%%[!$' \\t']*}\"} ;;\n  esac\ndone < \"$WORKBENCH_HEADERS\"\n")
	out.WriteString("case \"$WORKBENCH_UPLOAD_URL\" in *'#'*|*$'\\r'*|*$'\\n'*) printf '%s\\n' '上传 URL 无效' >&2; exit 1;; esac\ncase \"$WORKBENCH_UPLOAD_URL\" in " + shellLiteral(base.Scheme+"://"+base.Host+"/") + "*) ;; *) printf '%s\\n' '上传 URL 缺失或不属于当前 profile 的同源地址' >&2; exit 1;; esac\n")
	headers = nativeGeminiStageHeaders(spec, true)
	args = nativeCurlArgs(p, http.MethodPost, "\"$WORKBENCH_UPLOAD_URL\"", headers)
	args = append(args, "--data-binary \"@$FILE_1\"")
	out.WriteString(strings.Join(args, " \\\n  ") + "\n)\n")
	return out.String(), nil
}
