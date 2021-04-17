package frame

type Frame struct {
	Type string      `json:"type"`
	From string      `json:"from"`
	Data interface{} `json:"data"`
}

/*
### Receive
再生地点
{"from": "host", "type": "playbackPosition", "data": {"position": 11123, "currentTime": "2020-12-25 12:00:00"}}
同期ストップ
{"from": "client", "type": "command", "data": {"command": "syncStop"}}
同期スタート
{"from": "client", "type": "command", "data": {"command": "syncStart"}}

### Send
ルームを立てた際のルームIDの通知
{"from": "server", "type": "roomInfo", "data": {"roomID": "fgk3f79bgg"}}
クライアントの参加通知
{"from": "server", "type": "newClient", "data": {"clientCount": 32}}
ルームへの参加成功
{"from": "server", "type": "joinSuccess", "data": {"roomID": "fgk3f79bgg", "mediaURL": "https://youtube.com/..."}}

*/
