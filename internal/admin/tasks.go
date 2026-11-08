package admin

import (
	"context"
	"crypto/rand"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// accountTask 是在服务端运行的导入账号任务，管理页关闭或刷新后继续运行，结束后保留到用户关闭；Kind 为 chrome 或 cookie，State 为 running、done 或 failed
type accountTask struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	State     string            `json:"state"`
	Items     []accountTaskItem `json:"items"`
	StartedAt time.Time         `json:"started_at"`
}

// accountTaskItem 是任务中的一个账号，Label 为邮箱，State 为 pending、running、done 或 failed
type accountTaskItem struct {
	Label string `json:"label"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// accountTasks 保存导入账号任务
type accountTasks struct {
	mu      sync.Mutex
	entries []*accountTaskEntry
}

// accountTaskEntry 是登记中的任务与它的取消函数
type accountTaskEntry struct {
	task   accountTask
	cancel context.CancelFunc
}

// clone 返回不与登记中的任务共用账号列表的副本
func (task accountTask) clone() accountTask {
	task.Items = slices.Clone(task.Items)
	return task
}

// start 登记任务并在不随请求取消的 context 中依次以各账号运行 run，单个账号失败时记下原因后继续，任务被取消后停止；有账号失败时任务结束为 failed，否则为 done
func (tasks *accountTasks) start(parent context.Context, kind string, labels []string, run func(ctx context.Context, index int) error) accountTask {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	entry := &accountTaskEntry{task: accountTask{ID: rand.Text(), Kind: kind, State: "running", StartedAt: time.Now().UTC()}, cancel: cancel}
	for _, label := range labels {
		entry.task.Items = append(entry.task.Items, accountTaskItem{Label: label, State: "pending"})
	}
	update := func(change func(*accountTask)) {
		tasks.mu.Lock()
		defer tasks.mu.Unlock()
		change(&entry.task)
	}
	tasks.mu.Lock()
	tasks.entries = append(tasks.entries, entry)
	started := entry.task.clone()
	tasks.mu.Unlock()
	go func() {
		defer cancel()
		for index := range labels {
			if ctx.Err() != nil {
				break
			}
			update(func(task *accountTask) { task.Items[index].State = "running" })
			err := run(ctx, index)
			update(func(task *accountTask) {
				item := &task.Items[index]
				item.State = "done"
				if err != nil {
					item.State, item.Error = "failed", err.Error()
				}
			})
		}
		update(func(task *accountTask) {
			task.State = "done"
			if slices.ContainsFunc(task.Items, func(item accountTaskItem) bool { return item.State == "failed" }) {
				task.State = "failed"
			}
		})
	}()
	return started
}

// list 按开始顺序返回全部任务
func (tasks *accountTasks) list() []accountTask {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	list := make([]accountTask, len(tasks.entries))
	for index, entry := range tasks.entries {
		list[index] = entry.task.clone()
	}
	return list
}

// remove 取消进行中的任务或关闭已结束的任务，任务不存在时返回 false
func (tasks *accountTasks) remove(id string) bool {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	index := slices.IndexFunc(tasks.entries, func(entry *accountTaskEntry) bool { return entry.task.ID == id })
	if index < 0 {
		return false
	}
	tasks.entries[index].cancel()
	tasks.entries = slices.Delete(tasks.entries, index, index+1)
	return true
}

// removeTask 取消进行中的导入账号任务或关闭已结束的任务
func (h *handler) removeTask(c *gin.Context) {
	if !h.tasks.remove(c.Param("id")) {
		writeError(c, http.StatusNotFound, "task_not_found", "任务不存在")
		return
	}
	c.Status(http.StatusNoContent)
}
