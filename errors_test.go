// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

func TestDecodeErrorIsErrDecode(t *testing.T) {
	meta := registry.ByID(1)
	if meta == nil {
		t.Fatal("model 1 missing")
	}
	_, err := DecodeModel([]uint16{1, 0}, meta, 40000)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is(ErrDecode) = false, err=%v", err)
	}
	var dec *DecodeError
	if !errors.As(err, &dec) {
		t.Fatalf("errors.As(DecodeError) = false, err=%v", err)
	}
	if dec.ModelID != 1 {
		t.Fatalf("ModelID = %d, want 1", dec.ModelID)
	}
}

func TestErrPointNotFound(t *testing.T) {
	err := fmt.Errorf("%w: point %q in model %d", ErrPointNotFound, "W", 101)
	if !errors.Is(err, ErrPointNotFound) {
		t.Fatal("expected ErrPointNotFound")
	}
}

func TestPartialReadWrapsCause(t *testing.T) {
	cause := errors.New("modbus boom")
	err := fmt.Errorf("%w: read at %d+%d: %w", ErrPartialRead, 40000, 125, cause)
	if !errors.Is(err, ErrPartialRead) {
		t.Fatal("expected ErrPartialRead")
	}
	if !errors.Is(err, cause) {
		t.Fatal("expected underlying cause via errors.Is")
	}
}

func TestErrUnknownModel(t *testing.T) {
	d := &Device{Discovery: &DiscoveryResult{}}
	_, err := d.ReadModelByID(context.TODO(), 101)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("got %v, want ErrUnknownModel", err)
	}
}
