package adapter

import (
	"sync/atomic"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
)

// sharedStore 是文件、Responses 与视频接口共用的账本库，生成请求据此解析文件引用
var sharedStore atomic.Pointer[requestdb.Store]

// ConfigureStore 登记账本库并执行启动任务：恢复会话与冷却，把上次运行未完成的后台响应与视频标为失败，删除遗留的续传临时文件，从账本补入首事件样本
// 服务启动时在注册路由前调用一次
func ConfigureStore(pool *balancer.AccountPool, store *requestdb.Store) {
	sharedStore.Store(store)
	configureSessionStore(pool, store)
	configureCooldownStore(pool, store)
	failInterruptedResponses(store)
	failInterruptedVideos(store)
	removeStaleUploads(store)
	go firstEvents.seed(store)
}
