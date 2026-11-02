package op

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

// 并发累加必须保留每次更新的全部指标，首次创建和已有统计都要覆盖。
func TestStatsConcurrentUpdates(t *testing.T) {
	tests := []struct {
		name   string
		update func(int, model.StatsMetrics) error
		read   func(int) model.StatsMetrics
		remove func(int)
	}{
		{
			name:   "channel",
			update: StatsChannelUpdate,
			read: func(id int) model.StatsMetrics {
				return StatsChannelGet(id).StatsMetrics
			},
			remove: func(id int) {
				removeStatsTestID(statsChannelCache, &statsChannelCacheNeedUpdate, &statsChannelCacheNeedUpdateLock, id)
			},
		},
		{
			name: "model",
			update: func(id int, metrics model.StatsMetrics) error {
				return StatsModelUpdate(model.StatsModel{ID: id, StatsMetrics: metrics})
			},
			read: func(id int) model.StatsMetrics {
				stats, _ := statsModelCache.Get(id)
				return stats.StatsMetrics
			},
			remove: func(id int) {
				removeStatsTestID(statsModelCache, &statsModelCacheNeedUpdate, &statsModelCacheNeedUpdateLock, id)
			},
		},
		{
			name:   "api_key",
			update: StatsAPIKeyUpdate,
			read: func(id int) model.StatsMetrics {
				return StatsAPIKeyGet(id).StatsMetrics
			},
			remove: func(id int) {
				removeStatsTestID(statsAPIKeyCache, &statsAPIKeyCacheNeedUpdate, &statsAPIKeyCacheNeedUpdateLock, id)
			},
		},
	}

	const workers, increments = 16, 2000
	delta := model.StatsMetrics{
		InputToken: 3, OutputToken: 5, InputCost: 0.25, OutputCost: 0.5,
		WaitTime: 7, RequestSuccess: 1, RequestFailed: 2,
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for initial := 0; initial <= 1; initial++ {
				name := "missing"
				if initial == 1 {
					name = "existing"
				}
				t.Run(name, func(t *testing.T) {
					id := 100000001 + index*2 + initial
					t.Cleanup(func() { tt.remove(id) })
					if initial == 1 {
						if err := tt.update(id, delta); err != nil {
							t.Fatal(err)
						}
					}

					start := make(chan struct{})
					var wg sync.WaitGroup
					for worker := 0; worker < workers; worker++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							<-start
							for i := 0; i < increments; i++ {
								if err := tt.update(id, delta); err != nil {
									t.Error(err)
									return
								}
							}
						}()
					}
					close(start)
					wg.Wait()

					count := int64(workers*increments + initial)
					want := model.StatsMetrics{
						InputToken: delta.InputToken * count, OutputToken: delta.OutputToken * count,
						InputCost: delta.InputCost * float64(count), OutputCost: delta.OutputCost * float64(count),
						WaitTime: delta.WaitTime * count, RequestSuccess: count, RequestFailed: delta.RequestFailed * count,
					}
					if got := tt.read(id); got != want {
						t.Fatalf("concurrent updates lost metrics: got %+v, want %+v", got, want)
					}
				})
			}
		})
	}
}

// 控制首次未命中的返回时机，稳定复现“读取后补零值覆盖并发累加”的竞态。
type delayedStatsMissCache[V any] struct {
	cache.Cache[int, V]
	missed chan struct{}
	resume chan struct{}
	paused atomic.Bool
}

func (c *delayedStatsMissCache[V]) delayMiss(exists bool) {
	if !exists && c.paused.CompareAndSwap(false, true) {
		close(c.missed)
		<-c.resume
	}
}

func (c *delayedStatsMissCache[V]) Get(id int) (V, bool) {
	value, exists := c.Cache.Get(id)
	c.delayMiss(exists)
	return value, exists
}

func (c *delayedStatsMissCache[V]) GetOrSet(id int, value V) (V, bool) {
	value, exists := c.Cache.GetOrSet(id, value)
	c.delayMiss(exists)
	return value, exists
}

func TestStatsGetDoesNotOverwriteConcurrentUpdate(t *testing.T) {
	const id = 100000010
	t.Run("channel", func(t *testing.T) {
		original := statsChannelCache
		blocked := &delayedStatsMissCache[model.StatsChannel]{
			Cache: cache.New[int, model.StatsChannel](16), missed: make(chan struct{}), resume: make(chan struct{}),
		}
		statsChannelCache = blocked
		t.Cleanup(func() {
			removeStatsTestID(blocked, &statsChannelCacheNeedUpdate, &statsChannelCacheNeedUpdateLock, id)
			statsChannelCache = original
		})
		assertStatsGetterPreservesUpdate(t, blocked, func() { StatsChannelGet(id) }, func(metrics model.StatsMetrics) error {
			return StatsChannelUpdate(id, metrics)
		}, func() model.StatsMetrics {
			value, _ := blocked.Cache.Get(id)
			return value.StatsMetrics
		})
	})
	t.Run("api_key", func(t *testing.T) {
		original := statsAPIKeyCache
		blocked := &delayedStatsMissCache[model.StatsAPIKey]{
			Cache: cache.New[int, model.StatsAPIKey](16), missed: make(chan struct{}), resume: make(chan struct{}),
		}
		statsAPIKeyCache = blocked
		t.Cleanup(func() {
			removeStatsTestID(blocked, &statsAPIKeyCacheNeedUpdate, &statsAPIKeyCacheNeedUpdateLock, id)
			statsAPIKeyCache = original
		})
		assertStatsGetterPreservesUpdate(t, blocked, func() { StatsAPIKeyGet(id) }, func(metrics model.StatsMetrics) error {
			return StatsAPIKeyUpdate(id, metrics)
		}, func() model.StatsMetrics {
			value, _ := blocked.Cache.Get(id)
			return value.StatsMetrics
		})
	})
}

func assertStatsGetterPreservesUpdate[V any](t *testing.T, blocked *delayedStatsMissCache[V], get func(), update func(model.StatsMetrics) error, read func() model.StatsMetrics) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		get()
	}()
	release := sync.OnceFunc(func() { close(blocked.resume) })
	defer func() {
		release()
		<-done
	}()
	select {
	case <-blocked.missed:
	case <-time.After(5 * time.Second):
		t.Fatal("getter did not reach initial cache miss")
	}
	want := model.StatsMetrics{InputToken: 3, OutputToken: 5, InputCost: 0.25, OutputCost: 0.5, RequestSuccess: 1}
	if err := update(want); err != nil {
		t.Fatal(err)
	}
	release()
	<-done
	if got := read(); got != want {
		t.Fatalf("getter overwrote concurrent update: got %+v, want %+v", got, want)
	}
}

func removeStatsTestID[V any](valueCache cache.Cache[int, V], dirty *map[int]struct{}, dirtyLock *sync.Mutex, id int) {
	valueCache.Del(id)
	dirtyLock.Lock()
	delete(*dirty, id)
	dirtyLock.Unlock()
}
