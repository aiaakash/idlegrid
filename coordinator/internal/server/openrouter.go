package server

// OpenRouter provider integration: the model-catalog document their provider
// monitor polls (schema v2.4, https://openrouter.ai/docs/guides/community/for-providers).
//
//	GET /v1/openrouter/models   public — lists every model currently live
//
// OpenRouter routes real inference to the existing OpenAI-compatible
// POST /v1/chat/completions with an idlegrid API key it funds via top-up —
// no other changes needed; this endpoint only describes the supply.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"idlegrid/coordinator/store"
)

// microPer1MToCostUSD renders micro-USD-per-1M-tokens as OpenRouter's
// cost_usd string (USD per single token), exactly, via integer math.
// 50_000 micro/1M -> "0.00000005".
func microPer1MToCostUSD(microPer1M int64) string {
	if microPer1M <= 0 {
		return "0"
	}
	// USD/token = micro / 1e12. 12 fraction digits covers every representable value.
	digits := fmt.Sprintf("%012d", microPer1M)
	digits = strings.TrimRight(digits, "0")
	return "0." + digits
}

// HandleOpenRouterModels serves the OpenRouter provider-monitor document for
// every model the live fleet can serve right now. Public: their monitor has
// no credentials; the document only describes supply.
func (g *Gateway) HandleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	w.Header().Set("Content-Type", "application/json")

	models := g.Reg.OnlineModels()
	docs := make([]map[string]any, 0, len(models))
	for _, m := range models {
		inMicro, outMicro, err := store.PriceFor(r.Context(), g.Billing, m)
		if err != nil {
			inMicro, outMicro = store.DefaultInputMicroPer1M, store.DefaultOutputMicroPer1M
		}
		docs = append(docs, openRouterModelDoc(m, inMicro, outMicro))
	}
	json.NewEncoder(w).Encode(map[string]any{"data": docs})
}

func openRouterModelDoc(id string, inMicro, outMicro int64) map[string]any {
	doc := map[string]any{
		"schema_version": "2.4",
		"id":             id,
		"name":           id,
		"created":        time.Now().Unix(),
		// Every idlegrid model is served from an MLX 4-bit build today.
		"quantization": "int4",

		"input_modalities": []any{map[string]any{
			"type": "text",
			"pricing": []any{
				map[string]any{"type": "prompt", "unit": "token", "cost_usd": microPer1MToCostUSD(inMicro)},
			},
		}},
		"output_modalities": []any{map[string]any{
			"type":     "text",
			"streaming": true,
			"supported_parameters": map[string]any{
				"temperature": map[string]any{"type": "range", "min": 0, "max": 2},
				"top_p":       map[string]any{"type": "range", "min": 0, "max": 1},
				"max_tokens":  map[string]any{"type": "integer", "min": 1, "max": 8192},
				"stop":        map[string]any{"type": "array", "max_items": 4},
			},
			"pricing": []any{
				map[string]any{"type": "completion", "unit": "token", "cost_usd": microPer1MToCostUSD(outMicro)},
			},
		}},
		// Fallback-only until the fleet proves uptime — OpenRouter's monitor
		// needs 100+ requests before metrics count; is_ready publishes us.
		"is_ready": true,
	}
	// idlegrid models come straight from the Hugging Face Hub (MLX builds);
	// declaring the repo lets OpenRouter tie the document to the weights.
	if strings.Contains(id, "/") {
		doc["hugging_face_id"] = id
	}
	return doc
}
