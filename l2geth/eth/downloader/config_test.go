package downloader

import "testing"

func TestSetFastSyncFullBlocks(t *testing.T) {
	downloader := new(Downloader)
	if got := downloader.fastSyncFullBlockCount(); got != uint64(fsMinFullBlocks) {
		t.Fatalf("unexpected default full block count: %d", got)
	}
	if err := downloader.SetFastSyncFullBlocks(256); err != nil {
		t.Fatalf("set full block count: %v", err)
	}
	if got := downloader.fastSyncFullBlockCount(); got != 256 {
		t.Fatalf("unexpected configured full block count: %d", got)
	}
	if err := downloader.SetFastSyncFullBlocks(0); err == nil {
		t.Fatal("expected zero full block count to be rejected")
	}

	downloader.synchronising = 1
	if err := downloader.SetFastSyncFullBlocks(512); err != errBusy {
		t.Fatalf("expected errBusy while synchronising, got %v", err)
	}
}
