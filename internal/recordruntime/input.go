package recordruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

const maxPayloadBytes = 8 << 10

func inventorySchema(schema string) bool {
	return schema == "stock_received" || schema == "reservation_requested"
}

// LoadSource compiles an immutable handle without interpreting records or opening storage.
func LoadSource(files fs.FS, name string) (*jsonataddl.Application, error) {
	identity, err := Identity()
	if err != nil {
		return nil, err
	}
	app, err := jsonataddl.LoadApplication(files, ".", name, GitseqRecord(), identity.Digest())
	if err != nil {
		return nil, err
	}
	normalizer := app.NormalizerProgram()
	if len(normalizer.Reads) != 0 || len(normalizer.Writes) != 0 || len(app.Folds()) != 1 {
		return nil, errors.New("inventory requires a read-free, write-free normalizer and exactly one analytic fold")
	}
	return app, nil
}

func recordInput(record host.Record, position int) (jsonataddl.EvaluationInput, error) {
	if !inventorySchema(record.Schema) {
		return jsonataddl.EvaluationInput{}, errors.New("record is outside the inventory schema set")
	}
	if position < 1 {
		return jsonataddl.EvaluationInput{}, errors.New("record position must be positive")
	}
	if len(record.Payload) > maxPayloadBytes {
		return jsonataddl.EvaluationInput{}, errors.New("record payload exceeds 8192 bytes")
	}
	if !json.Valid(record.Payload) {
		return jsonataddl.EvaluationInput{}, errors.New("record payload must be one JSON value")
	}
	payload, err := jsonataddl.DecodeCanonical(record.Payload)
	if err != nil {
		return jsonataddl.EvaluationInput{}, err
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return jsonataddl.EvaluationInput{}, errors.New("record payload must be an object")
	}
	canonical, err := json.Marshal(object)
	if err != nil || !bytes.Equal(canonical, record.Payload) {
		return jsonataddl.EvaluationInput{}, errors.New("record payload must use canonical JSON bytes")
	}
	if len(object) != 3 {
		return jsonataddl.EvaluationInput{}, errors.New("inventory payload requires exactly id, sku and qty")
	}
	for _, column := range []jsonataddl.Column{{Name: "id", Type: jsonataddl.TypeText}, {Name: "sku", Type: jsonataddl.TypeText}, {Name: "qty", Type: jsonataddl.TypeInteger}} {
		if err := jsonataddl.ValidateValue(object[column.Name], column.Type, true); err != nil {
			return jsonataddl.EvaluationInput{}, fmt.Errorf("inventory payload %s: %w", column.Name, err)
		}
	}
	// Both current inventory event declarations require qty > 0. The core
	// owns integer/type validation; this is the closed application's CHECK.
	quantity, _ := object["qty"].(json.Number).Int64()
	if quantity <= 0 {
		return jsonataddl.EvaluationInput{}, errors.New("inventory payload qty must be positive")
	}
	digest := sha256.Sum256(record.Payload)
	return jsonataddl.EvaluationInput{
		Meta: map[string]any{},
		Rows: map[string]any{},
		Event: map[string]any{
			"id": record.ID, "schema": record.Schema, "actor": record.Actor,
			"position": strconv.Itoa(position), "timestamp": strconv.FormatInt(record.Timestamp, 10),
			"payload_digest": "sha256:" + hex.EncodeToString(digest[:]),
			"rests_on":       append([]string{}, record.RestsOn...), "payload": object,
		},
	}, nil
}
