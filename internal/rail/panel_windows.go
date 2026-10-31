//go:build windows

package rail

import (
	"acetool/internal/win"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// 细条上的「参数条」：点「连接」/「音素」弹出来，**就地**编辑那几项参数（用户 2026-10-17「搬进细条」），
// 不再绕到主面板去填。两种形态：
//
//	连接音符   = 一档一档的滑块（缝 ≤ 阈值才接）+「连接」
//	批量替换音素 = 查找 / 替换 两个文本框 +「替换」
//
// ⛔ 为什么它是**顶层小窗**，而不是像细条那样 `SetParent` 挂进 ACE 窗口（第五轮那个子窗口架构）：
// 输文本必须有**键盘焦点**；焦点是**按线程**分的、跟着**活动窗口**走，而子窗口自己不能被激活 ——
// 激活的永远是它所在的顶层窗口，也就是 **ACE（另一个进程）** ⇒ 我们的 EDIT 永远收不到键盘。
// 所以这一块只能做成"临时对话框"：弹出来、拿走键盘、Esc/回车收起；它有 `WS_EX_TOOLWINDOW`（不进任务栏），
// 关掉就销毁，不参与细条那套"永远贴 ACE"的层级逻辑。
//
// 值记在 exe 自己的 `%APPDATA%\ACETool\params.json`（见 `params.go`）——
// 主面板记的是它自己的 localStorage，两边**各记各的**，互不覆盖。

const (
	panelClass = "ACEToolRailPanel"
	panelWid   = 330 // 逻辑像素
)

// 参数条上的尺寸（**逻辑像素**，用时乘 scale）。
const (
	panelPad  = 10
	panelTtlH = 20
	panelHntH = 16
	panelRowH = 28
	panelBtnH = 26
)

// panelLayout 是参数条里每一块的矩形（**物理像素**，已经乘过 scale）。
type panelLayout struct {
	W, H   int32
	Title  rect
	Hint   rect
	Slider rect   // 只占滑轨那一条
	Value  rect   // 滑块右边的档位名
	Labels []rect // 每个输入框左边的标签（「查找」/「替换为」）
	Fields []rect
	Run    rect
}

// labelW 是输入框左边标签那一列的宽度（逻辑像素）。
const labelW = 54

// layoutOf 按条目与缩放算出整块面板的尺寸与各块位置。
func (r *Rail) layoutOf(it Item, scale float64) panelLayout {
	px := func(v int) int32 { return int32(float64(v) * scale) }
	var L panelLayout
	L.W = px(panelWid)
	y := px(panelPad)
	L.Title = rect{px(panelPad), y, L.W - px(panelPad), y + px(panelTtlH)}
	y += px(panelTtlH) + px(4)
	L.Hint = rect{px(panelPad), y, L.W - px(panelPad), y + px(panelHntH)}
	y += px(panelHntH) + px(8)
	if it.Panel == nil {
		return L
	}
	if it.Panel.Kind == ParamFields {
		L.Fields = make([]rect, 0, len(it.Panel.Fields))
		L.Labels = make([]rect, 0, len(it.Panel.Fields))
		lw := px(labelW)
		for range it.Panel.Fields {
			yBot := y + px(panelRowH)
			// 标签占左边一列、输入框占右边剩下的（跟主面板那排「查找/替换为」同一个排布）
			L.Labels = append(L.Labels, rect{px(panelPad), y, px(panelPad) + lw - px(6), yBot})
			L.Fields = append(L.Fields, rect{px(panelPad) + lw, y, L.W - px(panelPad), yBot})
			y = yBot + px(6)
		}
	} else {
		L.Slider = rect{px(panelPad), y, L.W - px(panelPad) - px(96), y + px(panelRowH)}
		L.Value = rect{L.Slider.Right + px(6), y, L.W - px(panelPad), y + px(panelRowH)}
		y += px(panelRowH) + px(8)
	}
	L.Run = rect{px(panelPad), y, px(panelPad) + px(88), y + px(panelBtnH)}
	y = L.Run.Bottom + px(panelPad)
	L.H = y
	return L
}

/* ============================ 附属参数（左键直接跑时用） ============================ */

// tierIndexOf 把"记着的档位值"映射回下标（记的值不在表里 ⇒ 第 0 档）。
func (r *Rail) tierIndexOf(it Item) int {
	if it.Panel == nil || it.Panel.Kind != ParamTier {
		return 0
	}
	cur := ParamGet(it.Tool, it.Panel.Arg)
	for k, t := range it.Panel.Tiers {
		if t.Value == cur {
			return k
		}
	}
	return 0
}

// tierValueOf 返回这一项**现在会用**的档位值（左键点击 = 用它去跑）。
func (r *Rail) tierValueOf(it Item) string {
	k := r.tierIndexOf(it)
	if it.Panel == nil || k >= len(it.Panel.Tiers) {
		return "0"
	}
	return it.Panel.Tiers[k].Value
}

// tierNameOf 返回这个档位**给人看**的名字（提示语/悬停全名里用）。
func (r *Rail) tierNameOf(it Item) string {
	if it.Panel == nil {
		return ""
	}
	k := r.tierIndexOf(it)
	if k >= len(it.Panel.Tiers) {
		return ""
	}
	return it.Panel.Tiers[k].Name
}

// itemTip 是悬停到某一行时显示的全名：
// 带附属参数的项要把**当前档位**和"怎么改"一起说出来（用户 2026-10-17：「参数只是附带」——
// 附带不等于看不见，否则同一个点击在不同档位下结果不同，用户会以为坏了）。
func (r *Rail) itemTip(it Item) string {
	if it.Panel == nil {
		return it.Full
	}
	if it.Panel.Kind == ParamTier {
		return it.Full + " · " + r.tierNameOf(it) + "（右键改）"
	}
	return it.Full + "（左键填参数）"
}

/* ============================ 开 / 关 ============================ */

// panelOpen 返回参数条现在开着哪一项（-1 = 没开）。
func (r *Rail) panelOpen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paramItem
}

// openPanel 打开第 i 项的参数条（**必须在消息循环线程上调用** —— 建窗口/拿焦点都认线程）。
func (r *Rail) openPanel(i int) {
	if i < 0 || i >= len(r.items) {
		return
	}
	it := r.items[i]
	if it.Panel == nil {
		return
	}
	_, scale := dpiScale()
	if r.paramHWND != 0 && r.paramItem != i {
		r.closePanel()
	}
	r.paramItem = i
	r.paramDrag = false
	r.paramActive = false

	// 档位：把记住的值映射回下标（记的值不在表里 ⇒ 回到第 0 档）
	r.paramTier = 0
	if it.Panel.Kind == ParamTier {
		cur := ParamGet(it.Tool, it.Panel.Arg)
		for k, t := range it.Panel.Tiers {
			if t.Value == cur {
				r.paramTier = k
				break
			}
		}
	}
	if r.paramHWND == 0 {
		if err := r.createPanelWindow(); err != nil {
			r.setErr(err)
			r.note(it.Full + "：参数条没建起来 —— " + err.Error())
			r.paramItem = -1
			return
		}
	}
	r.buildPanelControls(it, scale)
	L := r.layoutOf(it, scale)
	x, y := r.panelPos(L, scale)
	pSetWindowPos.Call(r.paramHWND, 0, uintptr(x), uintptr(y), uintptr(L.W), uintptr(L.H),
		uintptr(swpNoActivate|swpShowWindow|swpNoZOrder))
	// 拿走键盘：这一步**必须成功**，否则输入框收不到字（参数条就只剩滑块可用了）。
	// ⚠️ 不能只靠 `SetForegroundWindow`：我们从**定时器**里调它，Windows 的前台锁常常直接拒绝
	//（"只有刚收到输入的进程才能抢前台"）—— 实测用户正在别的程序里时就是这个结局。
	// ⇒ 用 `win.Focus`（把自己的输入队列附到当前前台线程上冒名，业界标准绕法）。
	if ok, ferr := win.Focus(r.paramHWND); !ok {
		r.setErr(ferr)
		pSetForegroundWindow.Call(r.paramHWND) // 再试一次朴素的（有时候反而是它行）
	}
	if len(r.paramEdits) > 0 {
		pSetFocus.Call(r.paramEdits[0])
	} else {
		pSetFocus.Call(r.paramHWND)
	}
	pInvalidateRect.Call(r.paramHWND, 0, 1)
	// 状态：自检要靠它看出"开的是哪一项、滑块第几档、窗口在哪、框里该是什么字"
	var texts []string
	if it.Panel.Kind == ParamFields {
		for _, f := range it.Panel.Fields {
			texts = append(texts, ParamGet(it.Tool, f.Name))
		}
	}
	r.mu.Lock()
	r.state.PanelItem, r.state.PanelTier = i, r.paramTier
	r.state.PanelHWND = r.paramHWND
	// 记下"按哪个矩形摆的"：用户拖/缩 ACE 窗口时细条的 rect 会变，
	// 自检如果拿**现在的** rect 去核**当时的**摆放就会假报错（真机踩到一次）。
	r.state.PanelAnchor = r.state.Rect
	r.state.PanelRect = win.Rect{Left: x, Top: y, Right: x + L.W, Bottom: y + L.H}
	r.state.PanelText = texts
	r.mu.Unlock()
}

// panelPos 把参数条摆在**细条左边**、与细条顶部对齐（超出屏幕就夹回来）。
func (r *Rail) panelPos(L panelLayout, scale float64) (int32, int32) {
	r.mu.Lock()
	rr := r.state.Rect
	r.mu.Unlock()
	x := rr.Left - L.W - int32(float64(8)*scale)
	if x < 0 {
		x = 0
	}
	y := rr.Top
	if rr.Top == 0 && rr.Bottom == 0 { // 细条还没定位（极少见）⇒ 就摆屏幕中间偏上
		x, y = int32(float64(160)*scale), int32(float64(160)*scale)
	}
	return x, y
}

func (r *Rail) createPanelWindow() error {
	if r.paramClass == nil {
		r.paramClass, _ = syscall.UTF16PtrFromString(panelClass)
	}
	wc := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   wndProcCB(),
		Instance:  r.hInst,
		ClassName: r.paramClass,
	}
	if ret, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 && !isClassExists(err) {
		return fmt.Errorf("RegisterClassExW（参数条）失败：%v", err)
	}
	title, _ := syscall.UTF16PtrFromString("ACETool 参数")
	h, _, err := pCreateWindowExW.Call(wsExTool|wsExTop, uintptr(unsafe.Pointer(r.paramClass)),
		uintptr(unsafe.Pointer(title)), wsPopup, 0, 0, 200, 120, 0, 0, r.hInst, 0)
	if h == 0 {
		return fmt.Errorf("CreateWindowExW（参数条）失败：%v", err)
	}
	r.paramHWND = h
	return nil
}

// buildPanelControls 按条目建/换那两个文本框（其它形态就不需要子控件）。
func (r *Rail) buildPanelControls(it Item, scale float64) {
	for _, e := range r.paramEdits {
		pDestroyWindow.Call(e)
	}
	r.paramEdits = nil
	if it.Panel == nil || it.Panel.Kind != ParamFields {
		return
	}
	font := makeFont(int(float64(13)*scale), fwNormal)
	editClass, _ := syscall.UTF16PtrFromString("EDIT")
	L := r.layoutOf(it, scale)
	for k, f := range it.Panel.Fields {
		val, _ := syscall.UTF16PtrFromString(ParamGet(it.Tool, f.Name))
		if k < len(L.Fields) {
			rc := L.Fields[k]
			h, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(editClass)), 0,
				uintptr(wsChild|wsVisible|wsBorder|wsTabStop|esLeft|esAutoHScroll),
				uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top),
				r.paramHWND, 0, r.hInst, 0)
			if h == 0 {
				continue
			}
			// 值在**建控件时**就给进去（`WM_SETTEXT`），免得多一次 SetWindowText
			if val != nil {
				pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(val)))
			}
			pSendMessageW.Call(h, wmSetFont, font, 1 /*TRUE：重画*/)
			// 子类化：Enter = 跑、Esc = 收、Tab = 下一个框。不这么做的话 EDIT 会把回车吃掉（还叫一声）
			old, _, _ := pSetWindowLongPtrW.Call(h, gwlWndProc, pPanelEditProc())
			r.paramEdits = append(r.paramEdits, h)
			r.mu.Lock()
			r.paramEditOld = append(r.paramEditOld, old)
			r.mu.Unlock()
		}
	}
}

// closePanel 收起参数条（销毁窗口与文本框）。
func (r *Rail) closePanel() {
	for _, e := range r.paramEdits {
		pDestroyWindow.Call(e)
	}
	r.paramEdits = nil
	r.mu.Lock()
	r.paramEditOld = nil
	r.paramItem = -1
	r.paramDrag = false
	r.state.PanelItem, r.state.PanelTier = -1, 0
	r.state.PanelHWND, r.state.PanelRect, r.state.PanelText, r.state.PanelAnchor = 0, win.Rect{}, nil, win.Rect{}
	r.mu.Unlock()
	if r.paramHWND != 0 {
		pDestroyWindow.Call(r.paramHWND)
		r.paramHWND = 0
	}
}

/* ============================ 跑 ============================ */

// runPanel 把参数条上的值发给工具（**先收起窗口**，再异步跑 —— HTTP 那步会阻塞）。
func (r *Rail) runPanel() {
	i := r.panelOpen()
	if i < 0 {
		return
	}
	it := r.items[i]
	p := it.Panel
	if p == nil {
		r.closePanel()
		return
	}
	vals := map[string]string{}
	for k, v := range it.Values {
		vals[k] = v
	}
	if p.Kind == ParamTier {
		if r.paramTier < 0 || r.paramTier >= len(p.Tiers) {
			r.paramTier = 0
		}
		vals[p.Arg] = p.Tiers[r.paramTier].Value
		if err := ParamSet(it.Tool, p.Arg, vals[p.Arg]); err != nil {
			railLog("参数没存住（%s.%s=%q）：%v —— 这次执行照旧，但重启后会回到旧值", it.Tool, p.Arg, vals[p.Arg], err)
		}
	} else {
		for k := range p.Fields {
			txt := ""
			if k < len(r.paramEdits) {
				txt = windowTextOf(r.paramEdits[k])
			}
			vals[p.Fields[k].Name] = txt
			if err := ParamSet(it.Tool, p.Fields[k].Name, txt); err != nil {
				railLog("参数没存住（%s.%s=%q）：%v —— 这次执行照旧，但重启后会回到旧值", it.Tool, p.Fields[k].Name, txt, err)
			}
		}
	}
	if vals["find"] != "" || p.Kind == ParamTier { // 音素的「查找」是空的就没意义，先挡一句（与主面板同一口径）
		// 留一条**审计记录**（谁在什么时候跑了什么、带什么值）：`%APPDATA%\ACETool\rail.log`
		railLog("跑 %s（%s）参数=%v", it.Tool, it.Full, vals)
		r.note(it.Full + "：开工…")
		go func() {
			resp, err := r.callTool(it.Tool, vals)
			if err != nil {
				r.note(it.Full + "：失败 —— " + err.Error())
				return
			}
			if !resp.OK {
				r.note(it.Full + "：失败 —— " + resp.reason())
				return
			}
			r.note(it.Full + "：" + resp.msg())
		}()
	} else {
		r.note(it.Full + "：先在参数条里填「查找」要换成哪个音素")
	}
	r.closePanel()
}

/* ============================ 画 ============================ */

func (r *Rail) paintPanel() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(r.paramHWND, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer pEndPaint.Call(r.paramHWND, uintptr(unsafe.Pointer(&ps)))
	i := r.panelOpen()
	if i < 0 || i >= len(r.items) || r.items[i].Panel == nil {
		return
	}
	it := r.items[i]
	p := it.Panel
	_, scale := dpiScale()
	L := r.layoutOf(it, scale)
	var full rect
	pGetClientRect.Call(r.paramHWND, uintptr(unsafe.Pointer(&full)))

	bg, _, _ := pCreateSolidBrush.Call(colorTipBG)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&full)), bg)
	pDeleteObject.Call(bg)
	drawBorder(hdc, full, colorBorder)
	pSetBkMode.Call(hdc, transparentBkMode)

	// 标题（全称）+ 一句说明
	fT := makeFont(int(float64(14)*scale), fwSemiBold)
	old, _, _ := pSelectObject.Call(hdc, fT)
	pSetTextColor.Call(hdc, colorText)
	r.text(hdc, it.Full, L.Title, dtLeft|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(fT)

	fH := makeFont(int(float64(11)*scale), fwNormal)
	old2, _, _ := pSelectObject.Call(hdc, fH)
	pSetTextColor.Call(hdc, colorDim)
	r.text(hdc, p.Hint, L.Hint, dtLeft|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
	pSelectObject.Call(hdc, old2)
	pDeleteObject.Call(fH)

	if p.Kind == ParamFields && len(L.Labels) > 0 {
		// 输入框左边的标签（「查找」/「替换为」）—— 不画的话没人分得清哪个框是哪个
		fL := makeFont(int(float64(12)*scale), fwNormal)
		oldL, _, _ := pSelectObject.Call(hdc, fL)
		pSetTextColor.Call(hdc, colorDim)
		for k := range p.Fields {
			if k < len(L.Labels) {
				r.text(hdc, p.Fields[k].Label, L.Labels[k], dtRight|dtVCenter|dtSingleLine|dtNoPrefix)
			}
		}
		pSelectObject.Call(hdc, oldL)
		pDeleteObject.Call(fL)
	}
	if p.Kind == ParamTier {
		// 滑轨 + 滑块 + 档位名
		track := rect{L.Slider.Left, (L.Slider.Top + L.Slider.Bottom) / 2, L.Slider.Right, (L.Slider.Top+L.Slider.Bottom)/2 + int32(float64(4)*scale)}
		tb, _, _ := pCreateSolidBrush.Call(colorBorder)
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&track)), tb)
		pDeleteObject.Call(tb)
		n := len(p.Tiers)
		if n > 1 {
			pad := int32(float64(7) * scale)
			pos := pad + int32(float64(track.Right-track.Left-2*pad)*float64(r.paramTier)/float64(n-1))
			thumb := rect{track.Left + pos - int32(float64(5)*scale), track.Top - int32(float64(7)*scale),
				track.Left + pos + int32(float64(5)*scale), track.Bottom + int32(float64(7)*scale)}
			hb, _, _ := pCreateSolidBrush.Call(colorParams)
			pFillRect.Call(hdc, uintptr(unsafe.Pointer(&thumb)), hb)
			pDeleteObject.Call(hb)
		}
		if r.paramTier >= 0 && r.paramTier < n {
			fV := makeFont(int(float64(12)*scale), fwNormal)
			oldV, _, _ := pSelectObject.Call(hdc, fV)
			pSetTextColor.Call(hdc, colorText)
			r.text(hdc, p.Tiers[r.paramTier].Name, L.Value, dtRight|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
			pSelectObject.Call(hdc, oldV)
			pDeleteObject.Call(fV)
		}
	}
	// 执行按钮
	bb, _, _ := pCreateSolidBrush.Call(colorHover)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&L.Run)), bb)
	pDeleteObject.Call(bb)
	drawBorder(hdc, L.Run, colorParams)
	if p.RunText != "" {
		fB := makeFont(int(float64(13)*scale), fwSemiBold)
		oldB, _, _ := pSelectObject.Call(hdc, fB)
		pSetTextColor.Call(hdc, colorText)
		r.text(hdc, p.RunText, L.Run, dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
		pSelectObject.Call(hdc, oldB)
		pDeleteObject.Call(fB)
	}
}

// drawBorder 画一圈 1px 边框（四块薄矩形拼起来）。
func drawBorder(hdc uintptr, rc rect, color uintptr) {
	b, _, _ := pCreateSolidBrush.Call(color)
	for _, e := range []rect{
		{rc.Left, rc.Top, rc.Right, rc.Top + 1},
		{rc.Left, rc.Bottom - 1, rc.Right, rc.Bottom},
		{rc.Left, rc.Top, rc.Left + 1, rc.Bottom},
		{rc.Right - 1, rc.Top, rc.Right, rc.Bottom},
	} {
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&e)), b)
	}
	pDeleteObject.Call(b)
}

// windowTextOf 读一个 EDIT 里的文字（读不到就是空串）。
func windowTextOf(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+2)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

/* ============================ 参数条的窗口过程 ============================ */

// panelProc 处理**参数条窗口自己**的消息（细条与栏外小窗以外的那一个）。
func (r *Rail) panelProc(hwnd uintptr, m uint32, w, l uintptr) (uintptr, bool) {
	if hwnd != r.paramHWND || hwnd == 0 {
		return 0, false
	}
	switch m {
	case wmPaint:
		r.paintPanel()
		return 0, true
	case wmCtlColorEdit:
		// 输入框的底色/字色（默认是**白底黑字**，在深色参数条上很扎眼）
		pSetTextColor.Call(w, colorText)
		pSetBkColor.Call(w, colorFieldBG)
		if r.fieldBrush == 0 {
			r.fieldBrush, _, _ = pCreateSolidBrush.Call(colorFieldBG)
		}
		return r.fieldBrush, true
	case wmEraseBkgnd:
		return 1, true
	case wmClose:
		r.closePanel()
		return 0, true
	case wmAppRun: // 见 wmAppRun 的注释：跑工具要**等这趟消息处理完**再动
		r.runPanel()
		return 0, true
	case wmActivate:
		// ⚠️ 这里**故意不自动收起**。原来写的是"失焦就收"，实测被前台竞赛打成筛子：
		// 全屏游戏 / 某些启动器会每隔几百毫秒把前台抢回去一次 ⇒ 参数条"闪一下就没"、
		// 用户根本来不及打字。参数条只是个**小对话框**，收起一律走**显式动作**：
		// Esc / 回车（跑）/ 再点细条上同一项 / 细条被藏起来（ACE 没了）/ 停侧栏。
		if w&0xffff != 0 {
			r.paramActive = true
			r.paramActiveAt = time.Now()
		}
		return 0, true
	case wmLButtonDown:
		x, y := int32(int16(l&0xffff)), int32(int16((l>>16)&0xffff))
		i := r.panelOpen()
		if i < 0 {
			return 0, true
		}
		it := r.items[i]
		_, scale := dpiScale()
		L := r.layoutOf(it, scale)
		if hit(L.Run, x, y) {
			// 记下"按下时在按钮上"，等**抬起还在按钮上**才真跑（标准按钮语义）——
			// 这样拖滑块拖出按钮、或者误按一下，都不会触发（工具是真写，不能靠运气）。
			r.paramRunDown = true
			return 0, true
		}
		if it.Panel != nil && it.Panel.Kind == ParamTier && hit(L.Slider, x, y) {
			r.paramDrag = true
			r.setTierFromX(L, x, scale)
			return 0, true
		}
		return 0, true
	case wmMouseMove:
		if r.paramDrag {
			x := int32(int16(l & 0xffff))
			i := r.panelOpen()
			if i >= 0 {
				_, scale := dpiScale()
				r.setTierFromX(r.layoutOf(r.items[i], scale), x, scale)
			}
			return 0, true
		}
	case wmLButtonUp:
		r.paramDrag = false
		if r.paramRunDown {
			// 按下时在「执行」按钮上、抬起也还在它上面 ⇒ 才真跑。
			// ⚠️ 不能直接 `runPanel()`：它会 `closePanel()`（销毁这个窗口），等于在自己的消息处理里
			// 把自己拆了 ⇒ 统一 post 给自己（`wmAppRun`）。
			i := r.panelOpen()
			if i >= 0 {
				x, y := int32(int16(l&0xffff)), int32(int16((l>>16)&0xffff))
				_, scale := dpiScale()
				if hit(r.layoutOf(r.items[i], scale).Run, x, y) {
					pPostMessageW.Call(r.paramHWND, wmAppRun, 0, 0)
				}
			}
			r.paramRunDown = false
		}
		return 0, true
	case wmKeyDown:
		switch int(w) {
		case 27: // Esc
			r.closePanel()
			return 0, true
		case 13: // Enter
			pPostMessageW.Call(r.paramHWND, wmAppRun, 0, 0)
			return 0, true
		case 37, 39: // ← →
			i := r.panelOpen()
			if i >= 0 {
				p := r.items[i].Panel
				if p != nil && p.Kind == ParamTier && len(p.Tiers) > 0 {
					if w == 37 {
						r.paramTier--
					} else {
						r.paramTier++
					}
					if r.paramTier < 0 {
						r.paramTier = 0
					}
					if r.paramTier >= len(p.Tiers) {
						r.paramTier = len(p.Tiers) - 1
					}
					r.mu.Lock()
					r.state.PanelTier = r.paramTier
					r.mu.Unlock()
					pInvalidateRect.Call(r.paramHWND, 0, 1)
				}
			}
			return 0, true
		}
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
	return ret, true
}

// setTierFromX 按鼠标横坐标定档位。
func (r *Rail) setTierFromX(L panelLayout, x int32, scale float64) {
	i := r.panelOpen()
	if i < 0 {
		return
	}
	tiers := r.items[i].Panel.Tiers
	if len(tiers) == 0 {
		return
	}
	pad := int32(float64(7) * scale)
	w := L.Slider.Right - L.Slider.Left - 2*pad
	if w <= 0 {
		return
	}
	f := float64(x-L.Slider.Left-pad) / float64(w)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	idx := int(f*float64(len(tiers)-1) + 0.5)
	if idx != r.paramTier {
		r.paramTier = idx
		r.mu.Lock()
		r.state.PanelTier = idx
		r.mu.Unlock()
		pInvalidateRect.Call(r.paramHWND, 0, 1)
	}
}

func hit(rc rect, x, y int32) bool {
	return x >= rc.Left && x < rc.Right && y >= rc.Top && y < rc.Bottom
}

/* ============================ 那两个输入框的子类化 ============================ */

var (
	editProcOnce sync.Once
	editProcAddr uintptr
)

// pPanelEditProc 是 EDIT 子类化用的那个**唯一**回调（跟 `wndProcCB` 同理：`syscall.NewCallback`
// 的回调表定长且永不释放，§72 就是被"每次新建"耗光的 —— 这里绝不能再犯）。
func pPanelEditProc() uintptr {
	editProcOnce.Do(func() { editProcAddr = syscall.NewCallback(panelEditProc) })
	return editProcAddr
}

// panelEditProc 是参数条里输入框的窗口过程（子类化）：
//
//	回车 = 跑 / Esc = 收 / Tab = 跳到下一个框
//
// 不这么做的话 EDIT 会把回车吃掉（还"叮"一声），用户只能去点那颗小按钮。
// ⚠️ 这里**只 post 消息**（`wmAppRun`/`wmClose`），不直接跑工具/销毁窗口 ——
// 在输入框自己的消息处理里把窗口拆了是自找麻烦。
func panelEditProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	r := theRail
	if r != nil && r.paramHWND != 0 {
		switch m {
		case wmKeyDown:
			switch int(w) {
			case 13: // Enter
				pPostMessageW.Call(r.paramHWND, wmAppRun, 0, 0)
				return 0
			case 27: // Esc
				pPostMessageW.Call(r.paramHWND, wmClose, 0, 0)
				return 0
			case 9: // Tab：在两个框之间跳
				r.mu.Lock()
				edits := append([]uintptr(nil), r.paramEdits...)
				r.mu.Unlock()
				for k, e := range edits {
					if e == hwnd {
						pSetFocus.Call(edits[(k+1)%len(edits)])
						return 0
					}
				}
				return 0
			}
		}
		// 交回它**原来的**窗口过程（EDIT 自己的编辑行为：光标、选择、输入法都靠它）
		r.mu.Lock()
		var old uintptr
		for k, e := range r.paramEdits {
			if e == hwnd && k < len(r.paramEditOld) {
				old = r.paramEditOld[k]
			}
		}
		r.mu.Unlock()
		if old != 0 {
			ret, _, _ := pCallWindowProcW.Call(old, hwnd, uintptr(m), w, l)
			return ret
		}
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
	return ret
}
