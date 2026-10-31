package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acetool/internal/acecli"
	"acetool/internal/tools"
)

// 面板只绑 127.0.0.1，但**本机其它网页**照样能对 localhost 发跨站请求。
// 所以所有能触发命令行执行的路径都要求一个自定义头 X-ACETool：
// 简单表单 / <img> / 普通 fetch 带不上它，浏览器会先发预检而我们不答 CORS ⇒ 被挡在门外。
// （本机自用的工具，这层足够；真要多用户再谈 token。）
const apiHeader = "X-ACETool"

const maxBodyBytes = 256 << 10

// codeHints 把 error.code 翻成**可执行的下一步**。
// 纪律 §10-6：失败要像错误报告 —— exit code / error.code / error.hint 原样展示，
// 这里补的是"那我该做什么"，不是把错误改写成"出错了"。
var codeHints = map[string]string{
	"NO_PROJECT_OPEN":            "ACE 里没有打开/新建工程 —— 先开一个再来。",
	"NOT_FOUND":                  "ID/uuid 不存在。⚠️ 用户换过工程后旧 uuid 会失效 ⇒ 先重新列举再抄。",
	"INVALID_ARG":                "参数或值被拒（ACE 越界一律拒绝、从不 clamp）。看 error.hint 里它实际接受什么，照它改，别猜。",
	"USER_BUSY":                  "宿主正忙（有一个打开的撤销括号，或正在导出/渲染）。可以带上 --wait-busy 有界等待后重试。",
	"STALE_WRITE":                "内容在你上次读取之后变了。重新读一次拿新 fingerprint，再写一次 —— 或故意去掉 --if-match 覆盖。",
	"FINGERPRINT_SCOPE_MISMATCH": "指纹作用域不对（拿 A 读出来的 token 去写 B）。跨作用域携带必然失败，去重读对应实体。",
	"CONFIRMATION_REQUIRED":      "破坏性命令没人确认，所以**什么都没跑**。加 -y 重跑。",
	"UNKNOWN_COMMAND":            "这个版本没有这条命令（surface 自述 pre-1.0、接口会变）。用 help --search <正则> 找替代，按能力降级。",
	"MEMBERSHIP_REQUIRED":        "该功能需要会员 —— 如实告诉用户，别当失败重试。",
	"CREDIT_INSUFFICIENT":        "额度不足 —— 如实告诉用户，别当失败重试。",
	"BRIDGE_UNREACHABLE":         "桥不可达：ACE 没开，或没在应用里打开 Connect to Agents。",
	"TIME_UNIT_REQUIRED":         "时间值缺单位（裸数字会被拒）：写 3840t / 1.5s / 2b / 2bar / 4.1.0 这种带单位的形式。",
	"HANDLER_FAILED":             "宿主端执行失败 —— 看 details（可能是磁盘这类可恢复原因，也可能是真 bug）。",
	"NOTE_OVERLAP":               "会让 Sing 片段里两个音符重叠 ⇒ 整笔拒绝、什么都不写。ACE 刻意不替你修剪：先把挡路的音符移走或删掉，作为它自己的一步。",
	"NO_PATTERN_EDIT_OPEN":       "这个操作需要一个片段在钢琴窗里打开（移动标记线就是打开动作）。",
	"EDITOR_NOT_OPEN":            "编辑器没打开 —— 先把标记线移进目标片段。",
}

func newMux(client *acecli.Client, from string) (*http.ServeMux, *panelState) {
	mux := http.NewServeMux()
	st := newPanelState(client, from)

	// 启动自检：找 CLI → 能力探测 → 在线灯。面板顶栏就吃这一份。
	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		force := r.URL.Query().Get("force") == "1"
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		writeJSON(w, checkUpdate(ctx, force))
	})
	mux.HandleFunc("/api/selfcheck", func(w http.ResponseWriter, r *http.Request) {
		// 自检要跑两条命令（各自 30 s 超时），给宽一点
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		rep := acecli.SelfCheck(ctx, client, from)
		writeJSON(w, struct {
			*acecli.SelfCheckReport
			App string `json:"app"`
		}{rep, appVersion})
	})

	// 工具清单（含表单 schema）—— 前端不写死任何工具。
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		catalog := tools.CatalogForUI()
		writeJSON(w, struct {
			tools.Catalog
			Writable bool   `json:"writable"`
			CLIPath  string `json:"cliPath"`
		}{catalog, tools.Writable(), client.Path})
	})

	// 执行一个工具。当前注册表全是只读工具；写工具会在这里被挡（M2 才开）。
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden,
				"缺 "+apiHeader+" 头 —— 面板自带的请求会带上它；这条限制是挡本机其它网页的跨站请求。")
			return
		}

		// ⚠️ `values` 用 map[string]any 收，再**一律归一成字符串**。
		// 起因是真踩到的一次：前端/脚本把 `absTick` 当**数字**发（`{"absTick": 29520}`），
		// 而工具签名收的是字符串 ⇒ 整个请求被拒成
		//   `请求体不是合法 JSON：json: cannot unmarshal number into Go struct field .values.absTick of type string`
		// —— 这句话既没说是哪个工具、也没说该怎么改，是**边界上的无谓陷阱**。
		// 表单值本来就是字符串（`<input>` 给的也是字符串），所以在这里宽进、工具那边保持简单。
		var req struct {
			ToolID string         `json:"toolId"`
			Values map[string]any `json:"values"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		values, err := stringifyValues(req.Values)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "values 里有没法当参数用的值："+err.Error())
			return
		}

		// 一份时间预算覆盖「读探针 + 跑调用」两段
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()

		// ⚠️ 执行体在 `panel_api.go` 的 `executeTool` 里：**系统级热键走的是同一个函数**
		// （另写一份的话，回包形状/干跑闸门/错误信封迟早会分叉）。
		resp := st.executeTool(ctx, req.ToolID, values)
		// ⭐ 任何一次写工具的执行都记下"最近一批"：侧栏也是走 /api/run（不是页面），
		// 不存的话「撤回这一批」在侧栏跑完后仍然是灰的（2026-10-22 用户实测）。
		if resp.Undo != nil {
			st.mu.Lock()
			st.lastUndo = resp.Undo
			st.mu.Unlock()
		}
		writeJSON(w, resp)
	})

	// 面板自己的那几个接口：快捷键 · 窗口置顶 · 事件（见 panel_api.go）
	st.registerPanelRoutes(mux)

	return mux, st
}

// runResponse 是一次工具执行的完整回包。
type runResponse struct {
	ToolID  string       `json:"toolId"`
	Title   string       `json:"title"`
	Kind    tools.Kind   `json:"kind"`
	CLIPath string       `json:"cliPath"`
	DryRun  bool         `json:"dryRun,omitempty"`
	OK      bool         `json:"ok"`
	Hint    string       `json:"hint,omitempty"`
	Plan    []tools.Call `json:"plan,omitempty"` // CLI 写工具的干跑计划
	Calls   []callResult `json:"calls"`
	// Undo 是「撤回刚才这一批」要用的信息（只在**写工具真写**时才有，见 panel_api.go 的
	// attachUndoMark）：跑之前的撤销栈游标 + 这一批压进去了几条。面板据此点亮撤回键。
	Undo *undoMark `json:"undo,omitempty"`
}

// undoMark：`anchor` 是那一批之前的撤销栈游标，`created` 是这一批新增的条目数 ——
// 退到 `anchor` 就说明这一批退干净了（判据在 internal/tools/history.go）。
type undoMark struct {
	Anchor  int      `json:"anchor"`
	Created int      `json:"created"`
	Names   []string `json:"names,omitempty"`
	Tool    string   `json:"tool"`
	Label   string   `json:"label"`
}

// stringifyValues 把 JSON 里的任意标量归一成**表单值的字符串**形式。
//
// 为什么宽进：工具签名只收 `map[string]string`（表单值的自然形状），
// 但调用方（手写脚本、以后的 UI）很容易把 `absTick` 当数字发。
// 原来那样会整个请求被拒，而且错误信息里连工具名都没有 —— 属于边界上的无谓陷阱。
//
// 规约（数字**不用科学计数法**，免得 tick 变成 `2.952e+04`）：
//
//	字符串 → 原样；数字 → 去掉多余小数（29520 不回 "29520.0"）；
//	布尔 → "1"/"0"（与前端 checkbox 一致）；null → ""
//
// 真正"没法当参数用"的只有嵌套对象/数组 —— 那种仍然报错，但**点名是哪个 key**。
func stringifyValues(in map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case nil:
			out[k] = ""
		case string:
			out[k] = t
		case bool:
			if t {
				out[k] = "1"
			} else {
				out[k] = "0"
			}
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("字段 %q 是 %T；values 里只能是字符串/数字/布尔", k, v)
		}
	}
	return out, nil
}

// inProc 收一个**进程内**实现（读或写/干跑）的结果，统一成一条 callResult。
// 这类工具没有 argv 可展示，所以 Args 只放工具 id —— 标签里会写明它没经过 CLI。
func (r *runResponse) inProc(label, toolID string, v any, err error) {
	if err != nil {
		r.OK = false
		r.Hint = err.Error()
		r.Calls = append(r.Calls, callResult{
			Label: label, Args: []string{toolID}, ExitCode: 1,
			// ⚠️ 这个 code 是**我们自己**的（不是 ACE 的信封）⇒ 必须中性且准确。
			// 原先写死成 `ACEP_UNREADABLE`，那是**文件路线**（`.acep` 读不动）的说法；
			// 而这条路径同样跑 CLI 工具（`track.split` / `xcopy.notes`）——
			// 于是"轨道下标填错了"会显示成 `error[ACEP_UNREADABLE]`，纯误导。
			Error: &acecli.Envelope{Code: "TOOL_FAILED", Message: err.Error()},
		})
		return
	}
	body, merr := json.MarshalIndent(v, "", "  ")
	if merr != nil {
		r.OK = false
		r.Hint = merr.Error()
		return
	}
	r.Calls = append(r.Calls, callResult{Label: label, Args: []string{toolID}, ExitCode: 0, JSON: body})
}

type callResult struct {
	Label    string           `json:"label"`
	Args     []string         `json:"args"`
	ExitCode int              `json:"exitCode"`
	Millis   int64            `json:"millis,omitempty"`
	TimedOut bool             `json:"timedOut,omitempty"`
	Stdout   string           `json:"stdout,omitempty"`
	Stderr   string           `json:"stderr,omitempty"`
	JSON     json.RawMessage  `json:"json,omitempty"`
	Error    *acecli.Envelope `json:"error,omitempty"`
	SpawnErr string           `json:"spawnError,omitempty"`
}

func newCallResult(label string, r *acecli.Result) callResult {
	return callResult{
		Label:    label,
		Args:     r.Args,
		ExitCode: r.ExitCode,
		Millis:   r.Millis,
		TimedOut: r.TimedOut,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		JSON:     r.JSON,
		Error:    r.Err,
		SpawnErr: r.SpawnErr,
	}
}

// explain 是失败时给用户的「下一步」。优先 error.hint（那是权威描述），
// 再叠我们按 error.code 写的处置建议，最后兜一层退出码语义。
func explain(r *acecli.Result) string {
	var parts []string
	if r.Err != nil {
		if h := codeHints[r.Err.Code]; h != "" {
			parts = append(parts, h)
		}
	}
	switch r.ExitCode {
	case acecli.ExitUsage:
		parts = append(parts, "exit 2 = 用法错：CLI 解析层就失败了，**命令根本没跑到 ACE**。用 help 核对语法。")
	case acecli.ExitBridgeDown:
		parts = append(parts, "exit 3 = 桥不可达：启动 ACE Studio（若已在跑就重启），再重试。")
	case acecli.ExitSpawnFailed:
		parts = append(parts, "我们这边连进程都没起来 —— 先看去顶栏的 CLI 路径对不对。")
	}
	if r.TimedOut {
		parts = append(parts, fmt.Sprintf("调用超时（%d ms）后被终止。", r.Millis))
	}
	if r.Err != nil && r.Err.Hint != "" {
		parts = append(parts, "ACE 自己的提示："+r.Err.Hint)
	}
	return strings.Join(parts, "  ")
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// isTruthy 判定表单里的"勾上了"。**干跑闸门就靠它**（`values["dryRun"]`）。
//
// ⚠️ 这里的失败方式是**危险方向**的：认不出来 ⇒ 当成"没勾" ⇒ **真的去写**。
// 所以肯定式收得宽（含中文的 是/有/开 —— 与 tools 包 boolWant 的口径一致），
// 否定式也收（0/false/off/no/n/否/无/关）；**其余一律当假**（宁可多干跑，不可误写）。
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "yes", "y", "是", "有", "开", "真":
		return true
	}
	return false
}
