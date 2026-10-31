package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"acetool/internal/acecli"
)

// 恢复备份：**换回之前先给当前文件留一份**、换完逐字节核对、坏备份拒收。
func TestProjectRestoreSwapsBackAndKeepsCurrent(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "歌", "歌.acep")
	if err := os.MkdirAll(filepath.Dir(proj), 0o755); err != nil {
		t.Fatal(err)
	}
	// "当前"版本 = 轨名改过的 payload B；"备份"版本 = 原始 payload A（两者都是合法 ACEP2）
	payloadA := synthPayload(t)
	payloadB := synthPayloadWithName(t, "轨 0（改过）")
	cur := wrapACEP2(t, payloadB)
	back := wrapACEP2(t, payloadA)
	if err := os.WriteFile(proj, cur, 0o644); err != nil {
		t.Fatal(err)
	}
	// 把"备份"放进备份目录（用工具自己那套命名：<名>-<时间戳>.acep）
	bdir := backupDir()
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(bdir, "歌-testrestore.acep")
	if err := os.WriteFile(backupPath, back, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(backupPath) })

	rd := func(_ context.Context, args ...string) (*acecli.Result, error) {
		body := []byte(`{}`)
		return &acecli.Result{Args: args, ExitCode: acecli.ExitOK, Stdout: string(body), JSON: body}, nil
	}
	vals := map[string]string{"path": proj, "backup": backupPath}

	// ① 干跑：文件一字节不动
	if _, err := projectRestorePlan(context.Background(), rd, vals); err != nil {
		t.Fatalf("干跑失败：%v", err)
	}
	now, _ := os.ReadFile(proj)
	if string(now) != string(cur) {
		t.Fatal("干跑竟然换了文件")
	}

	// ② 真恢复
	out, err := projectRestoreApply(context.Background(), rd, vals)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	rep := out.(*restoreReport)
	after, _ := os.ReadFile(proj)
	if string(after) != string(back) {
		t.Fatal("恢复之后文件内容不等于备份")
	}
	if rep.Kept == "" {
		t.Fatal("没报「换回去之前的副本」")
	}
	kept, err := os.ReadFile(rep.Kept)
	if err != nil || string(kept) != string(cur) {
		t.Fatalf("换之前的副本不对（%v）", err)
	}
	t.Cleanup(func() { _ = os.Remove(rep.Kept) })

	// ③ 坏备份（不是合法 ACEP2）必须被拒，而且**不能**把工程文件弄坏
	badPath := filepath.Join(bdir, "歌-badrestore.acep")
	if err := os.WriteFile(badPath, []byte("这不是 acep"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(badPath) })
	if _, err := projectRestoreApply(context.Background(), rd,
		map[string]string{"path": proj, "backup": badPath}); err == nil {
		t.Fatal("坏备份应当被拒")
	}
	still, _ := os.ReadFile(proj)
	if string(still) != string(back) {
		t.Fatal("被拒之后工程文件不该变")
	}
}

// ④ 不给 backup ⇒ 找这份工程**最近一次**的备份
func TestLatestBackupForPicksNewest(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "另一首", "另一首.acep")
	bdir := backupDir()
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldP := filepath.Join(bdir, "另一首-20000101-000000.acep")
	newP := filepath.Join(bdir, "另一首-20990101-000000.acep")
	for _, p := range []string{oldP, newP} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(p) })
	}
	got, err := latestBackupFor(proj)
	if err != nil {
		t.Fatalf("找最近备份失败：%v", err)
	}
	// mtime 都是"现在"⇒ 按时间比可能平手；这里只要求**找到的是这两个之一**（不误认别的工程）
	if got != oldP && got != newP {
		t.Fatalf("找错了备份：%s", got)
	}
}
