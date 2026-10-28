package httpapi

import (
	"net/http"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/adapter"
)

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	// 只列已实现的模型：M1 仅转写，TTS 系列在 M2 上线后再加入，
	// 避免客户端拿到会 404 的模型 ID。
	writeJSON(w, http.StatusOK, modelsResponse{
		Object: "list",
		Data: []modelObject{
			{ID: adapter.ASRModel, Object: "model", Created: 0, OwnedBy: "xiaomi"},
		},
	})
}
