package gemini

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/tidwall/sjson"
)

type FileData struct {
	URL      string
	FileName string
}

type ChatMetadata struct {
	CID  string
	RID  string
	RCID string
}

// BuildGeneratePayload constructs the 'f.req' parameter.
// Python logic:
// json.dumps([
//
//	None,
//	json.dumps([
//	    [prompt, 0, null, image_list, ...],
//	    None,
//	    chat_metadata
//	]),
//	None,
//	None
//
// ])
func BuildGeneratePayload(prompt string, reqID int, files []FileData, meta *ChatMetadata) string {
	imagesJSON := `[]`
	if len(files) > 0 {
		for i, f := range files {
			item := `[]`
			urlArr := `[]`
			urlArr, _ = sjson.Set(urlArr, "0", f.URL)
			urlArr, _ = sjson.Set(urlArr, "1", 1)

			item, _ = sjson.SetRaw(item, "0", urlArr)
			item, _ = sjson.Set(item, "1", f.FileName)

			imagesJSON, _ = sjson.SetRaw(imagesJSON, fmt.Sprintf("%d", i), item)
		}
	}

	msgStruct := `[]`
	msgStruct, _ = sjson.Set(msgStruct, "0", prompt)
	msgStruct, _ = sjson.Set(msgStruct, "1", 0)
	msgStruct, _ = sjson.Set(msgStruct, "2", nil)
	msgStruct, _ = sjson.SetRaw(msgStruct, "3", imagesJSON)
	msgStruct, _ = sjson.Set(msgStruct, "4", nil)
	msgStruct, _ = sjson.Set(msgStruct, "5", nil)
	msgStruct, _ = sjson.Set(msgStruct, "6", nil)

	inner := `[]`
	inner, _ = sjson.SetRaw(inner, "0", msgStruct)
	inner, _ = sjson.Set(inner, "1", nil)

	if meta != nil {
		metaArr := `[]`
		metaArr, _ = sjson.Set(metaArr, "0", meta.CID)
		metaArr, _ = sjson.Set(metaArr, "1", meta.RID)
		metaArr, _ = sjson.Set(metaArr, "2", meta.RCID)
		inner, _ = sjson.SetRaw(inner, "2", metaArr)
	} else {
		inner, _ = sjson.Set(inner, "2", nil)
	}

	// Pad to index 7 and set streaming flag
	for i := 3; i < 7; i++ {
		inner, _ = sjson.Set(inner, fmt.Sprintf("%d", i), nil)
	}
	inner, _ = sjson.Set(inner, "7", 1) // Enable Snapshot Streaming

	outer := `[null, "", null, null]`
	outer, _ = sjson.Set(outer, "1", inner)

	return outer
}

func GenerateReqID() int {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(100000) + 100000
}
