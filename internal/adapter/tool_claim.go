package adapter

import (
	"fmt"
	"regexp"
	"strings"
)

// toolQuotaClaims 匹配声称工具调用次数用尽、为零或工具无法调用的句子：用尽、零或否定标记与工具名词出现在同一句
var toolQuotaClaims = []*regexp.Regexp{
	regexp.MustCompile(`(无法|不能|没法|没有办法)[^。！？\n]{0,6}(调用|使用)[^。！？\n]{0,6}工具`),
	regexp.MustCompile(`(?i)\b(cannot|can't|can not|unable to|no longer able to)\s+(call|use|invoke)\s+(the\s+|any\s+)?tools?\b`),
	regexp.MustCompile("(工具|函数|调用)[^。！？\\n]{0,12}(次数|额度|配额|上限|限额)[^。！？\\n]{0,12}(用尽|耗尽|用完|已经?达|为 ?`?0|剩余 ?`?0|只剩 ?`?0)"),
	regexp.MustCompile("剩余[^。！？\\n]{0,12}(工具|调用)[^。！？\\n]{0,8}(额度|次数)[^。！？\\n]{0,6}`?0"),
	regexp.MustCompile("(已经?|由于|因为)达到?了?[^。！？\\n]{0,6}(工具|函数)[^。！？\\n]{0,4}调用[^。！？\\n]{0,4}(次数|额度|配额)?(上限|限制|限额)"),
	regexp.MustCompile(`(?i)(tool|function)[ -]?(calls?|uses?|usage|invocations?)[^.!?\n]{0,40}(limit|quota|budget|allowance)[^.!?\n]{0,30}(reached|exhausted|exceeded|used up|hit|depleted)`),
	regexp.MustCompile(`(?i)(exhausted|used up|run out of|ran out of)[^.!?\n]{0,40}(tool|function)[ -]?(calls?|uses?)`),
	regexp.MustCompile(`(?i)(maximum|max) (allowed |number of )?(tool|function)[ -]?(calls?|uses?|usage)[^.!?\n]{0,30}(reached|exhausted|exceeded|hit)`),
	regexp.MustCompile(`(?i)(reached|hit|exceeded) the (maximum|max|limit)[^.!?\n]{0,30}(tool|function)[ -]?(calls?|uses?)`),
	regexp.MustCompile(`(?i)(no|zero|0) (more |remaining )?(tool|function)[ -]?calls? (left|remaining|available)`),
}

// toolRefusalPredicate 匹配自称无法访问或执行的谓语，否定词与动作之间允许一段不跨分句的状语
var toolRefusalPredicate = regexp.MustCompile(`(?i)(无法|不能|没有办法|没法|不具备|没有权限|无权)[^。！？；，,\n]{0,20}(访问|执行|读取|查看|操作|运行|连接|获取|调用|打开|写入|修改|删除)|\bi (cannot|can't|can not|am unable to|'m unable to|am not able to|do not have access|don't have access|have no access)|\b(cannot|can't|unable to) (directly )?(access|execute|run|read|open|modify|write)\b`)

// toolRefusalTarget 匹配被推脱的对象：用户的本地环境
var toolRefusalTarget = regexp.MustCompile(`(?i)本地|本机|文件系统|终端|命令行|操作系统|(你|您)的(电脑|设备|系统|环境|文件)|local (files?|machine|computer|environment|system)|file ?system|your (computer|machine|device|system|files|environment|terminal)|terminal|shell|command line`)

// toolRefusalMaxRunes 是判为能力拒答的正文长度上限，更长的正文是在正常作答
const toolRefusalMaxRunes = 1200

// misreportsTools 报告没有工具调用的正文是否声称工具调用次数用尽，或以无法访问本地环境为由不调用工具
func misreportsTools(text string) bool {
	if matches(toolQuotaClaims, text) {
		return true
	}
	return len([]rune(text)) <= toolRefusalMaxRunes && toolRefusalPredicate.MatchString(text) && toolRefusalTarget.MatchString(text)
}

// toolExcuseOpening 报告正文开头是否出现了声称或拒答的谓语，出现时扣住正文直到回答结束再判定
func toolExcuseOpening(text string) bool {
	return matches(toolQuotaClaims, text) || toolRefusalPredicate.MatchString(text)
}

// toolCorrection 返回重新生成时附在提示词末尾的纠正说明，列出本次请求的真实工具名
func (b ToolBridge) toolCorrection() string {
	names := make([]string, 0, len(b.Definitions))
	for _, definition := range b.Definitions {
		names = append(names, definition.Name)
	}
	return fmt.Sprintf("上一次回答声称工具调用次数已用尽或无法使用工具，这不成立，该回答已作废：可用工具由客户端在用户的环境中执行，调用没有次数限制，官网内置工具的次数提示不适用于它们。可用工具：%s。助手现在按工具协议写出完成任务需要的调用。", strings.Join(names, "、"))
}
