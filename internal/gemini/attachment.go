package gemini

import (
	"path"
	"slices"
	"strings"
)

// attachmentMIMETypes 是官网上传组件按 MIME 类型标注的附件类型编号中与前缀规则不同的项
var attachmentMIMETypes = map[string]int{
	"application/dart":                        3,
	"application/ecmascript":                  3,
	"application/ms-java":                     3,
	"application/protobuf":                    3,
	"application/sql":                         3,
	"application/typescript":                  3,
	"application/vnd.apache.parquet":          3,
	"application/vnd.curl":                    3,
	"application/vnd.dart":                    3,
	"application/vnd.ibm.secure-container":    3,
	"application/vnd.jupyter":                 3,
	"application/x-csh":                       3,
	"application/x-latex":                     3,
	"application/x-php":                       3,
	"application/x-powershell":                3,
	"application/x-sh":                        3,
	"application/x-shellscript":               3,
	"application/x-tex":                       3,
	"application/x-zsh":                       3,
	"application/xml":                         3,
	"application/json":                        5,
	"application/vnd.google-apps.ritz":        6,
	"application/vnd.google-apps.spreadsheet": 6,
	"application/vnd.ms-excel":                7,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": 7,
	"application/vnd.google-apps.document":                              8,
	"application/vnd.google-apps.kix":                                   8,
	"application/gzip":                                                  9,
	"application/x-gzip":                                                9,
	"application/zip":                                                   9,
	"application/msword":                                                10,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": 10,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.template": 10,
	"application/pdf": 11,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": 12,
	"application/vnd.google-apps.presentation":                                  13,
	"application/vnd.google-apps.punch":                                         13,
	"application/vnd.oasis.opendocument.text":                                   14,
	"application/x-hwp":                                                         15,
	"application/x-hwp-v5":                                                      15,
	"application/vnd.google-apps.folder":                                        17,
	"message/rfc822":                                                            19,
}

// codeFileNames 是官网上传组件按代码文件处理的文件名
var codeFileNames = []string{"BUILD", "makefile"}

// codeFileExtensions 是官网上传组件按代码文件处理的扩展名
var codeFileExtensions = strings.Fields("asm asset bib boo c c++ cc ccc clang-format class cls coffee conf config cpp cql cs csh css cu cuh curl cxx d dart el erl es ets flake8 gitignore go gv h h++ hh hpp hrl hs htc htm html hxx in ini ipynb jad java js json json5 jsx jtd kt ktm lhs local lsp ltx lua m manifest map md meta metal mjs mm mo n3 p pas patch php pie pl pm po properties ps1 py r rb rmd rs s sample sass sc scala scm scss sgm sgml sh shaclc shex shtml si sl sos spo sql sty swift symbols t tag tcl tk tm toml tr ts tsx ttl types uri uris vue wgsl wml wmls xml yaml yml zone zsh")

// attachmentType 按官网上传组件的规则返回生成请求中附件的类型编号：代码文件为 16，其余按 MIME 类型，文本 3、图片 1、音频 4、视频 2、未知 0
func attachmentType(mimeType string, fileName string) int {
	if slices.Contains(codeFileNames, path.Base(fileName)) {
		return 16
	}
	if dot := strings.LastIndex(fileName, "."); dot > 0 && slices.Contains(codeFileExtensions, strings.ToLower(fileName[dot+1:])) {
		return 16
	}
	if code, ok := attachmentMIMETypes[mimeType]; ok {
		return code
	}
	switch {
	case strings.HasPrefix(mimeType, "text/"):
		return 3
	case strings.HasPrefix(mimeType, "image/"):
		return 1
	case strings.HasPrefix(mimeType, "audio/"):
		return 4
	case strings.HasPrefix(mimeType, "video/"):
		return 2
	}
	return 0
}
