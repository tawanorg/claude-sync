package webdav

import (
	"encoding/xml"
	"net/url"
	"strings"
	"time"
)

type propfindMultistatus struct {
	XMLName   xml.Name             `xml:"multistatus"`
	Responses []propfindRespEntry  `xml:"response"`
}

type propfindRespEntry struct {
	Href     string            `xml:"href"`
	Propstat []propfindPropstat `xml:"propstat"`
}

type propfindPropstat struct {
	Prop   propfindProp `xml:"prop"`
	Status string       `xml:"status"`
}

type propfindProp struct {
	ContentLength int64            `xml:"getcontentlength"`
	LastModified  string           `xml:"getlastmodified"`
	ETag          string           `xml:"getetag"`
	ResourceType  propResourceType `xml:"resourcetype"`
}

type propResourceType struct {
	Collection *struct{} `xml:"collection"`
}

type parsedResponse struct {
	Href          string
	ContentLength int64
	LastModified  time.Time
	ETag          string
	IsCollection  bool
}

func parsePropfindResponse(data []byte) ([]parsedResponse, error) {
	var ms propfindMultistatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, err
	}

	var results []parsedResponse
	for _, entry := range ms.Responses {
		r := parsedResponse{}

		href := entry.Href
		if decoded, err := url.PathUnescape(href); err == nil {
			href = decoded
		}
		r.Href = href

		for _, ps := range entry.Propstat {
			if !strings.Contains(ps.Status, "200") {
				continue
			}
			r.ContentLength = ps.Prop.ContentLength
			r.ETag = strings.Trim(ps.Prop.ETag, "\"")
			r.IsCollection = ps.Prop.ResourceType.Collection != nil

			if ps.Prop.LastModified != "" {
				for _, layout := range []string{
					time.RFC1123,
					time.RFC1123Z,
					"Mon, 02 Jan 2006 15:04:05 GMT",
					time.RFC3339,
				} {
					if t, err := time.Parse(layout, ps.Prop.LastModified); err == nil {
						r.LastModified = t
						break
					}
				}
			}
		}

		results = append(results, r)
	}

	return results, nil
}
