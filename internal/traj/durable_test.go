package traj

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestAppendDurablePersistsAndRejectsCanceledWrite(t *testing.T) {
	tl := newTimeline(t)
	step := NewStep(TypeMessage)
	step.Fields["content"] = "持久消息"
	if err := tl.AppendWithOptions(context.Background(), step, AppendOptions{Durable: true}); err != nil {
		t.Fatal(err)
	}
	steps, err := tl.Steps()
	if err != nil || len(steps) != 2 || steps[1].StepID != step.StepID {
		t.Fatalf("steps=%v err=%v", steps, err)
	}
	before, err := os.ReadFile(tl.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tl.AppendWithOptions(ctx, NewStep(TypeMessage), AppendOptions{Durable: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	after, err := os.ReadFile(tl.Path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("取消后仍写入: %v", err)
	}
}

type recordWriter struct {
	written           []byte
	syncs             int
	writeErr, syncErr error
	short             bool
}

func (w *recordWriter) Write(b []byte) (int, error) {
	w.written = append(w.written, b...)
	if w.short {
		return len(b) - 1, nil
	}
	return len(b), w.writeErr
}
func (w *recordWriter) Sync() error { w.syncs++; return w.syncErr }

func TestWriteRecordReportsDurabilityFailures(t *testing.T) {
	fault := errors.New("测试 I/O 故障")
	for _, tc := range []struct {
		name    string
		durable bool
		writer  recordWriter
		wantErr bool
		syncs   int
	}{
		{"durable", true, recordWriter{}, false, 1},
		{"normal", false, recordWriter{syncErr: fault}, false, 0},
		{"sync-failure", true, recordWriter{syncErr: fault}, true, 1},
		{"write-failure", true, recordWriter{writeErr: fault}, true, 0},
		{"short-write", true, recordWriter{short: true}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := writeRecord(&tc.writer, []byte("消息\n"), tc.durable)
			if (err != nil) != tc.wantErr || tc.writer.syncs != tc.syncs {
				t.Fatalf("err=%v syncs=%d", err, tc.writer.syncs)
			}
			if !tc.wantErr && string(tc.writer.written) != "消息\n" {
				t.Fatalf("写入内容=%q", tc.writer.written)
			}
			if (tc.writer.writeErr != nil || tc.writer.syncErr != nil) && tc.wantErr && !errors.Is(err, fault) {
				t.Fatalf("错误原因丢失: %v", err)
			}
		})
	}
}
