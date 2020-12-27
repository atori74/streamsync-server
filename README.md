# streamsync-server

This is a websocket server for StreamSync, google chrome extension.

### StreamSync

StreamSync is a google chrome extension.
This enables to sync playback position between Host (browser) and Client (browser).

### How StreamSync works

Host(chrome) -> (data) -> WS Server -> (data) -> Client(chrome)

Host can open a room and Client can join it.
Host send playback position data of video or streaming playing in browser to the room.
Clients in the same room receive the data and automatically sync the playback position of video or streaming. 

Host and Client have websocket connection with server as long as they are in the room.

