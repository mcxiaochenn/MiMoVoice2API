package httpapi

import (
	"net/http"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/adapter"
)

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	// 只列已实现的模型；M3 的流式能力复用同一批模型，不新增 ID。
	writeJSON(w, http.StatusOK, modelsResponse{
		Object: "list",
		Data: []modelObject{
			{ID: adapter.ASRModel, Object: "model", Created: 0, OwnedBy: "xiaomi"},
			{ID: adapter.TTSModel, Object: "model", Created: 0, OwnedBy: "xiaomi"},
			{ID: adapter.TTSVoiceDesignModel, Object: "model", Created: 0, OwnedBy: "xiaomi"},
			{ID: adapter.TTSVoiceCloneModel, Object: "model", Created: 0, OwnedBy: "xiaomi"},
		},
	})
}
