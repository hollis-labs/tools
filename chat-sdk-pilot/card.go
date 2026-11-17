package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/libs/agent/sdk/session"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
)

// publishCard stores an immutable card projection and commits its reference to
// chathost. The committed event cursor, not the card file, orders view replay.
func (p *pilot) publishCard(ctx context.Context, id string, card any, approval bool) error {
	data, err := json.Marshal(card)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	dir := filepath.Join(p.root, "cards")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, digest+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(existing, data) {
			return fmt.Errorf("pilot: card digest collision or unreadable file")
		}
	} else if err != nil {
		return err
	} else {
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
	}
	kind := session.Event
	if approval {
		kind = session.Approval
	}
	_, err = p.host.Admit(principal(ctx), session.Message{
		Binding: p.binding, ID: id, Kind: kind,
		Reference: runtimecontract.Reference{
			ID: "pilot-card:" + digest, Revision: "1", IssuerRef: "pilot-card-store", OwnerID: ownerID,
			Digest: runtimecontract.Digest{Scheme: "sha256", Value: digest},
		},
	})
	return err
}

func (p *pilot) card(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	attachment, ok := p.attachment(r.URL.Query().Get("id"))
	if !ok || attachment.Binding != p.binding {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
		return
	}
	hash := r.URL.Query().Get("hash")
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid card hash"})
		return
	}
	data, err := os.ReadFile(filepath.Join(p.root, "cards", hash+".json"))
	if err != nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "card not found"})
		return
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != hash {
		jsonReply(w, http.StatusConflict, map[string]string{"error": "card digest mismatch"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
