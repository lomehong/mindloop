// models.go 是模型目录探测：UI"从提供商拉取"用它把档案的 models
// 清单一次填好。一次性调用、不重试、不缓存——拉不到直接报错，用户
// 手动重试；缓存（TTL）归 web 层，本包保持无状态。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ListModels 探测供应商的模型目录，按目录顺序返回模型 id。
// openai-compatible: GET {base}/models；anthropic: GET {base}/v1/models
// （两家响应同为 {"data":[{"id":...}]} 形态；anthropic 的分页只取
// 第一页）。无 key 也发请求：本地网关（Ollama/vLLM）无鉴权可用，
// 401 也能更快暴露。echo 返回固定单例。
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c.Provider == ProviderEcho {
		return []string{"echo"}, nil
	}
	var url string
	headers := map[string]string{}
	switch c.Provider {
	case ProviderAnthropic:
		url = c.BaseURL + "/v1/models"
		headers["anthropic-version"] = "2023-06-01"
		if c.APIKey != "" {
			headers["x-api-key"] = c.APIKey
		}
	case ProviderOpenAICompat:
		url = c.BaseURL + "/models"
		if c.APIKey != "" {
			headers["Authorization"] = "Bearer " + c.APIKey
		}
	default:
		return nil, fmt.Errorf("llm: 未知供应商 %q", c.Provider)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: 构造请求: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: 网络错误: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: 读取响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("llm: 解析模型目录: %w (%s)", err, snippet(data))
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
