package service

import (
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const imageCallStatsDocumentName = "image_call_stats.json"

type ImageCallStatsService struct {
	mu       sync.Mutex
	store    storage.JSONDocumentBackend
	fallback string
	data     map[string]any
}

func NewImageCallStatsService(dataDir string, backend storage.Backend) *ImageCallStatsService {
	s := &ImageCallStatsService{
		store:    jsonDocumentStoreFromBackend(backend),
		fallback: dataDir + "/image_call_stats.json",
		data:     map[string]any{},
	}
	s.data = normalizeImageCallStats(loadStoredJSON(s.store, imageCallStatsDocumentName, s.fallback))
	return s
}

func (s *ImageCallStatsService) Record(count int) {
	if count < 1 {
		count = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	days := imageStatsDays(s.data["days"])
	today := statsToday()
	s.data["initialized"] = true
	s.data["total_calls"] = util.ToInt(s.data["total_calls"], 0) + count
	days[today] = util.ToInt(days[today], 0) + count
	s.data["days"] = days
	s.data["updated_at"] = statsNow()
	_ = s.saveLocked()
}

func (s *ImageCallStatsService) Snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	days := imageStatsDays(s.data["days"])
	return map[string]any{
		"today_calls": util.ToInt(days[statsToday()], 0),
		"total_calls": util.ToInt(s.data["total_calls"], 0),
		"updated_at":  util.Clean(util.ValueOr(s.data["updated_at"], statsNow())),
	}
}

func (s *ImageCallStatsService) saveLocked() error {
	return saveStoredJSON(s.store, imageCallStatsDocumentName, s.fallback, s.data)
}

func normalizeImageCallStats(raw any) map[string]any {
	obj := util.StringMap(raw)
	days := imageStatsDays(obj["days"])
	return map[string]any{
		"initialized": util.ToBool(obj["initialized"]),
		"total_calls": maxInt(0, util.ToInt(obj["total_calls"], 0)),
		"days":        days,
		"updated_at":  util.Clean(obj["updated_at"]),
	}
}

func imageStatsDays(value any) map[string]any {
	days := map[string]any{}
	if raw, ok := value.(map[string]any); ok {
		for key, item := range raw {
			days[key] = maxInt(0, util.ToInt(item, 0))
		}
	}
	return days
}

func statsToday() string {
	return time.Now().In(shanghaiTZ).Format("2006-01-02")
}

func statsNow() string {
	return time.Now().In(shanghaiTZ).Format("2006-01-02 15:04:05")
}
