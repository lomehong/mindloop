package wecom

// 会话与标记的持久化状态（StateDir 下）：全部是 bridge 私有的投递
// 优化态，丢失的代价是降级（不流式/重复推送），不是正确性破坏。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

// session 是一条入站回调的关联：轨迹步骤 id（responder 回复用
// reply_to 引用它）↔ 渠道回调的 req_id（被动流式通道的凭证）。
// req_id 只在内存与 StateDir 里——桥重启丢失后流式降级为主动推送。
type session struct {
	ReqID  string `json:"req_id"`
	Userid string `json:"userid"`
}

// sessionStore 是 stepID → session 的持久化映射（容量封顶，先进
// 先出淘汰——会话只在回复窗口内有价值，旧条目自然失效）。
type sessionStore struct {
	mu   sync.Mutex
	path string
	m    map[string]session
}

const sessionCap = 200

func newSessionStore(path string) *sessionStore {
	st := &sessionStore{path: path, m: map[string]session{}}
	st.load()
	return st
}

func (st *sessionStore) load() {
	data, err := os.ReadFile(st.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &st.m)
}

func (st *sessionStore) put(stepID string, sess session) {
	st.mu.Lock()
	if st.m == nil {
		st.m = map[string]session{}
	}
	st.m[stepID] = sess
	// 容量淘汰：超出上限时删最早写入的键（map 无序，取任意超量
	// 键删除即可——会话淘汰不需要精确 FIFO）。
	for len(st.m) > sessionCap {
		for k := range st.m {
			delete(st.m, k)
			break
		}
	}
	st.mu.Unlock()
	st.save()
}

func (st *sessionStore) get(stepID string) (session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.m[stepID]
	return sess, ok
}

// has 报告步骤 id 是否有已跟踪会话（出站泵 Skip 谓词用）。
func (st *sessionStore) has(stepID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.m[stepID]
	return ok
}

func (st *sessionStore) save() {
	data, err := json.Marshal(st.m)
	if err != nil {
		return
	}
	_ = os.WriteFile(st.path, data, 0o600)
}

// streamedSet 是已流式送达终稿的回复步骤 id 集合：出站泵据此跳过
// （终稿已经 respond 通道全量推送过，再走 send_msg 就是重复消息）。
// 标记在发 finish=true **之前**写入——崩溃窗口宁丢终稿不重摄。
type streamedSet struct {
	mu   sync.Mutex
	path string
	m    map[string]bool
}

func newStreamedSet(path string) *streamedSet {
	st := &streamedSet{path: path, m: map[string]bool{}}
	if data, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(data, &st.m)
	}
	return st
}

// mark 先记后用：返回 true 表示本次调用完成了标记（首次）。
func (st *streamedSet) mark(replyStepID string) bool {
	st.mu.Lock()
	first := !st.m[replyStepID]
	if first {
		st.m[replyStepID] = true
		if data, err := json.Marshal(st.m); err == nil {
			_ = os.WriteFile(st.path, data, 0o600)
		}
	}
	st.mu.Unlock()
	return first
}

func (st *streamedSet) has(replyStepID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.m[replyStepID]
}

// unmark 撤销标记（finish 推送 ack 失败时交还出站泵补发终稿）。
func (st *streamedSet) unmark(replyStepID string) {
	st.mu.Lock()
	delete(st.m, replyStepID)
	if data, err := json.Marshal(st.m); err == nil {
		_ = os.WriteFile(st.path, data, 0o600)
	}
	st.mu.Unlock()
}

// tokenStore 是审批卡短 token → 脚本哈希的持久化映射：卡片按钮的
// key 有长度上限，装不下 64 位 sha256；点击回调带短 token，桥本地
// 换回真哈希再写决策。token 只增不删（决策文件过期即自然失效）。
type tokenStore struct {
	mu   sync.Mutex
	path string
	m    map[string]string // token -> hash
}

func newTokenStore(path string) *tokenStore {
	st := &tokenStore{path: path, m: map[string]string{}}
	if data, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(data, &st.m)
	}
	return st
}

// mint 为哈希铸造（或复用）一个 8 字节 hex 短 token。
func (st *tokenStore) mint(hash string) string {
	st.mu.Lock()
	for token, h := range st.m {
		if h == hash {
			st.mu.Unlock()
			return token
		}
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	token := hex.EncodeToString(rnd[:])
	st.m[token] = hash
	if data, err := json.Marshal(st.m); err == nil {
		_ = os.WriteFile(st.path, data, 0o600)
	}
	st.mu.Unlock()
	return token
}

// resolve 把点击回调的短 token 换回脚本哈希；未知 token 返回空串。
func (st *tokenStore) resolve(token string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.m[token]
}
