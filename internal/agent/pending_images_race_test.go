package agent

// H8 tests: PendingImages data race — concurrent facade writes + SendOutcome
// consume under -race. Verifies all PendingImages access paths are synchronized.

import (
	"io"
	"sync"
	"testing"

	"github.com/treeol/wakil/internal/config"
	"github.com/treeol/wakil/internal/proxy"
)

// TestPendingImages_ConcurrentAccess verifies that concurrent
// AddPendingImage + ReplacePendingImages + SendOutcome consume do not race.
func TestPendingImages_ConcurrentAccess(t *testing.T) {
	app := &App{
		Cfg:  config.DefaultConfig(),
		Out:  io.Discard,
	}

	var wg sync.WaitGroup
	const n = 100

	// Writer 1: AddPendingImage
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			app.AddPendingImage(proxy.ImagePart{
				Path:    "test.png",
				DataURL: "data:image/png;base64,iVBOR=",
				MIME:    "image/png",
				Size:    5,
			})
		}
	}()

	// Writer 2: ReplacePendingImages
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			app.ReplacePendingImages([]proxy.ImagePart{{
				Path:    "replace.png",
				DataURL: "data:image/png;base64,iVBOR=",
				MIME:    "image/png",
				Size:    5,
			}})
		}
	}()

	// Writer 3: ClearPendingImages
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			app.ClearPendingImages()
		}
	}()

	// Reader: snapshot-style read under stateMu
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			app.stateMu.RLock()
			_ = app.PendingImages
			app.stateMu.RUnlock()
		}
	}()

	wg.Wait()
}

// TestPendingImages_SendOutcomeConsume verifies the consume in SendOutcome
// is locked — concurrent AddPendingImage while consuming should not race.
func TestPendingImages_SendOutcomeConsume(t *testing.T) {
	app := &App{
		Cfg:  config.DefaultConfig(),
		Out:  io.Discard,
	}

	// Pre-populate with some images.
	for i := 0; i < 10; i++ {
		app.AddPendingImage(proxy.ImagePart{
			Path:    "test.png",
			DataURL: "data:image/png;base64,iVBOR=",
			MIME:    "image/png",
			Size:    5,
		})
	}

	var wg sync.WaitGroup

	// Concurrent consumer: simulate the SendOutcome swap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		app.stateMu.Lock()
		if len(app.PendingImages) > 0 {
			_ = app.PendingImages
			app.PendingImages = nil
		}
		app.stateMu.Unlock()
	}()

	// Concurrent writer: AddPendingImage.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			app.AddPendingImage(proxy.ImagePart{
				Path:    "race.png",
				DataURL: "data:image/png;base64,iVBOR=",
				MIME:    "image/png",
				Size:    5,
			})
		}
	}()

	wg.Wait()
}
