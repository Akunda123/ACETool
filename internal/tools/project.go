package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"acetool/internal/acep"
)

// 「改工程文件」那条流程要用的三个小工具（用户 2026-10-08 定的顺序）：
//
//	① `project.save`（写）—— 让宿主落盘（技能 acep §8 第 ① 步：先让宿主落盘，再动文件）
//	② `project.path`（读）—— 问出**当前工程的文件路径**（改文件必须知道改哪个）
//	③ `project.open`（写）—— 改完让 ACE **重新打开**那份文件（技能 §8 第 ④ 步）
//
// ⚠️ 它们走的是 CLI（不是文件），所以有撤销栈/指纹那套；真正的文件改写是 `pitch.within` 干的事。
func init() {
	register(&Tool{
		ID: "project.save", Category: "工程",
		Title: "保存工程",
		Desc: "让 ACE 把当前工程落盘（`project save`），随后回读一次工程状态。" +
			"⚠️ 写：默认干跑。文件路线的**第一步** —— 没落盘就改文件，ACE 里的内存状态会盖掉你的改动" +
			"（技能 acep §8：① 先让宿主落盘）。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §8 纪律第 ① 条",
		Params: nil,
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			return []Call{
				{Label: "保存工程", Args: []string{"project", "save"}},
				{Label: "回读工程状态", Args: []string{"project", "dirty"}},
			}, nil
		},
	})

	register(&Tool{
		ID: "project.path", Category: "工程",
		Title: "当前工程的路径与状态（只读）",
		Desc: "读出**当前工程的文件路径** + `dirty` + `isTempProject` + 名字 —— 文件路线要知道改哪个文件。" +
			"⚠️ 路径来自 `project dirty`（`project info` 没有 path/dirty 两个键）。" +
			"没保存过的工程（临时工程）拿不到路径 ⇒ 先在 ACE 里「另存为」。只读。",
		Kind:   KindRead,
		Source: "技能 acep v1.2.3 §5.3 / §5.2（工程要成\"包\"才有路径）",
		Params: nil,
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return readProjectPath(ctx, rd)
		},
	})

	register(&Tool{
		ID: "project.open", Category: "工程",
		Title: "重新打开工程（改完文件之后）",
		Desc: "让 ACE 重新打开指定路径的 `.acep`（`project open <path> --discard-changes`）。" +
			"文件路线**最后一步**：文件改了，ACE 那份还是旧内存状态，不重载看不到" +
			"（技能 acep §8 第 ④ 步）。会丢掉未保存改动（所以流程要求先「保存工程」）。\n" +
			"⚠️ **实测（2026-10-15）**：重开会把 ACE 的**共享撤销栈整个清掉**（`history list` **6 条 → 0 条**）" +
			"⇒ 这一步之后 **Ctrl+Z 回不到以前**（连你自己之前的编辑步也没了）；" +
			"文件路线的还原**只能**靠「恢复备份」。\n" +
			"⚠️ 另外它**会清空编辑器选区**（选区不在文件里）⇒ 要接着操作先在 ACE 里重新选。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §5.3（project open 的语义）",
		Params: []Param{
			textParam("path", "工程文件路径", "", `C:\…\名字\名字.acep`, "要重新打开的那份；一般直接传「保存工程」给出来的路径"),
		},
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			p := strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
			if p == "" {
				return nil, fmt.Errorf("要一个工程文件路径（path）")
			}
			return []Call{
				{Label: "重新打开工程", Args: []string{"project", "open", p, "--discard-changes"}},
				{Label: "回读工程状态", Args: []string{"project", "info"}},
			}, nil
		},
	})
	register(&Tool{
		ID: "project.restore", Category: "工程",
		Title: "恢复备份（把临时目录里的备份换回来）",
		Desc: "把 `%TEMP%\\ACETool\\acep-backup\\` 里的某份备份**换回**工程文件（文件路线的后悔药：\n" +
			"用户 2026-10-08「在临时文件夹存一个工程备份以便恢复刚才的操作」）。\n" +
			"不给 `backup` ⇒ 取**这份工程最近的**一次备份。恢复是写操作，所以：先给**当前**文件也留一份备份" +
			"（`<名>-<时间戳>-before-restore.acep`），再原地替换，然后回读核对（大小 + sha256）。\n" +
			"⚠️ 恢复完要**让 ACE 重新打开**（本工具只动文件；页面会自动调 `project.open`）。默认干跑。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §8 纪律（备份 / 只改必要字节 / 读回核对）",
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`, "要恢复的那份工程"),
			textParam("backup", "备份文件（留空 = 这份工程最近的一次）", "", `%TEMP%\ACETool\acep-backup\…`,
				"留空时按**同名工程**在备份目录里找最新的那份"),
		},
		Plan:  projectRestorePlan,
		Apply: projectRestoreApply,
	})
}

// ---------- 恢复备份 ----------

type restoreReport struct {
	DryRun bool     `json:"干跑"`
	Path   string   `json:"工程文件"`
	Backup string   `json:"用哪份备份"`
	Mtime  string   `json:"备份时间"`
	Size   string   `json:"大小"`
	Kept   string   `json:"换回去之前又留的一份,omitempty"`
	Checks []string `json:"自检,omitempty"`
	Hint   string   `json:"提示,omitempty"`
}

// backupDir 是备份目录（与 `pitch.within` 写备份时同一个地方）。
func backupDir() string { return filepath.Join(os.TempDir(), "ACETool", "acep-backup") }

// latestBackupFor 找这份工程**最近一次**的备份（按文件名前缀 + mtime）。
func latestBackupFor(projPath string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(projPath), ".acep")
	ents, err := os.ReadDir(backupDir())
	if err != nil {
		return "", fmt.Errorf("备份目录读不到（%s）—— 还没做过带备份的操作？", backupDir())
	}
	best, bestT := "", time.Time{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".acep") {
			continue
		}
		if !strings.HasPrefix(e.Name(), base+"-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestT) {
			best, bestT = filepath.Join(backupDir(), e.Name()), info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("备份目录里没有「%s」的备份（%s）", base, backupDir())
	}
	return best, nil
}

func resolveRestore(vals map[string]string) (proj, backup string, err error) {
	proj = strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
	if proj == "" {
		return "", "", fmt.Errorf("要一个工程文件路径（path）")
	}
	if _, err := os.Stat(proj); err != nil {
		return "", "", fmt.Errorf("读不到工程文件：%w", err)
	}
	backup = strings.Trim(strings.TrimSpace(value(vals, "backup", "")), `"`)
	if backup == "" {
		backup, err = latestBackupFor(proj)
		if err != nil {
			return "", "", err
		}
	}
	if _, err := os.Stat(backup); err != nil {
		return "", "", fmt.Errorf("读不到备份文件：%w", err)
	}
	return proj, backup, nil
}

func projectRestorePlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	proj, backup, err := resolveRestore(vals)
	if err != nil {
		return nil, err
	}
	bs, _ := os.Stat(backup)
	ps, _ := os.Stat(proj)
	rep := &restoreReport{
		DryRun: true, Path: proj, Backup: backup,
		Mtime: bs.ModTime().Format("2006-01-02 15:04:05"),
		Size:  fmt.Sprintf("备份 %d B → 工程 %d B", bs.Size(), ps.Size()),
		Hint: "干跑：**一个字节都没换**。取消勾选「干跑」才会把备份换回工程文件" +
			"（换之前会给当前文件也留一份备份）。",
	}
	return rep, nil
}

func projectRestoreApply(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	proj, backup, err := resolveRestore(vals)
	if err != nil {
		return nil, err
	}
	want, err := os.ReadFile(backup)
	if err != nil {
		return nil, err
	}
	// ⛔ **先验备份是不是合法工程**，再动当前文件 —— 实测踩过：先换再验的话，
	// 一个坏备份会把工程文件换成垃圾（虽然报错，但文件已经毁了）。
	if _, err := acep.Parse(want); err != nil {
		return nil, fmt.Errorf("这份备份不是合法的 .acep（%v）—— **拒绝恢复**，工程文件一个字节没动", err)
	}
	// 换回去之前，先给**当前**文件留一份（万一这个备份也不是你要的）
	cur, err := os.ReadFile(proj)
	if err != nil {
		return nil, err
	}
	kept, err := writeBackupWithSuffix(proj, cur, "-before-restore")
	if err != nil {
		return nil, fmt.Errorf("留当前文件失败，**不恢复**：%w", err)
	}
	// 原地替换（同目录临时文件 + rename）
	tmp := proj + ".acetool-restore"
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, proj); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	// 回读核对：逐字节比
	got, err := os.ReadFile(proj)
	if err != nil {
		return nil, fmt.Errorf("换完回读失败（备份在 %s）: %w", kept, err)
	}
	if len(got) != len(want) || sha256.Sum256(got) != sha256.Sum256(want) {
		return nil, fmt.Errorf("换完回读**对不上**（又留了一份换之前的：%s）", kept)
	}
	// 能解成 ACEP2 才算数（防止把一个坏文件换回去）
	if _, err := acep.Open(proj); err != nil {
		return nil, fmt.Errorf("换回去的文件 ACE 壳校验没过（%v）—— 换之前的副本：%s", err, kept)
	}
	bs, _ := os.Stat(backup)
	rep := &restoreReport{
		Path: proj, Backup: backup, Kept: kept,
		Mtime: bs.ModTime().Format("2006-01-02 15:04:05"),
		Size:  fmt.Sprintf("恢复 %d B", len(got)),
		Checks: []string{
			"回读逐字节一致（sha " + shaOf(got) + "）",
			"ACEP2 壳校验通过",
			"换之前的当前文件也留了一份：" + kept,
		},
		Hint: "已把备份换回工程文件。下一步：让 ACE 重新打开它（页面会自动做）。",
	}
	return rep, nil
}

func writeBackupWithSuffix(proj string, raw []byte, suffix string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(proj), ".acep")
	stamp := time.Now().Format("20060102-150405")
	dst := filepath.Join(backupDir(), base+"-"+stamp+suffix+".acep")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return "", err
	}
	back, err := os.ReadFile(dst)
	if err != nil {
		return "", err
	}
	if len(back) != len(raw) || sha256.Sum256(back) != sha256.Sum256(raw) {
		return "", fmt.Errorf("备份回读不一致（%s）", dst)
	}
	return dst, nil
}

// ⚠️ 字段名按技能记的来，但**读不出来就如实留空**（不同版本可能多/少键）。
type projState struct {
	Path          string `json:"path"`
	ProjectPath   string `json:"projectPath"`
	ProjectName   string `json:"projectName"`
	Dirty         *bool  `json:"dirty"`
	IsTempProject *bool  `json:"isTempProject"`
	IsNewProject  *bool  `json:"isNewProject"`
	SavedPath     string `json:"savedPath"`
}

type projPathReport struct {
	Path        string `json:"工程文件路径"`
	Dirty       string `json:"未保存改动"`
	TempProject string `json:"是不是临时工程"`
	Name        string `json:"工程名"`
	Note        string `json:"说明"`
}

func readProjectPath(ctx context.Context, rd ReadFunc) (*projPathReport, error) {
	var st projState
	// 路径/dirty 在 `project dirty` 里（技能 §5.3 记的）；info 顺手补名字。
	if err := readJSON(ctx, rd, &st, "project", "dirty"); err != nil {
		return nil, err
	}
	var info projState
	if err := readJSON(ctx, rd, &info, "project", "info"); err == nil {
		if st.ProjectName == "" {
			st.ProjectName = info.ProjectName
		}
		if st.Path == "" {
			st.Path = info.Path
		}
		if st.ProjectPath == "" {
			st.ProjectPath = info.ProjectPath
		}
	}
	rep := &projPathReport{}
	p := firstNonEmpty(st.Path, st.ProjectPath, st.SavedPath)
	rep.Path = p
	rep.Name = st.ProjectName
	if st.Dirty == nil {
		rep.Dirty = "(读不出来)"
	} else if *st.Dirty {
		rep.Dirty = "有（先「保存工程」再改文件）"
	} else {
		rep.Dirty = "没有（已落盘）"
	}
	if st.IsTempProject == nil {
		rep.TempProject = "(读不出来)"
	} else if *st.IsTempProject {
		rep.TempProject = "是（**没保存过** ⇒ 拿不到路径，先在 ACE 里另存为）"
	} else {
		rep.TempProject = "不是"
	}
	if p == "" {
		rep.Note = "⚠️ 没拿到路径 —— 这份工程可能还没保存过（临时工程）。在 ACE 里「另存为」一个**工程包**" +
			"（`<名字>\\<名字>.acep` + `autosave\\`），再来。「保存工程」也会把路径带出来。"
	} else {
		rep.Note = "路径来自 `project dirty`（`project info` 没有 path/dirty 这两个键）。"
	}
	return rep, nil
}

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
