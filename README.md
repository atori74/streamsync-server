# streamsync-server

This is a websocket server for StreamSync, google chrome extension.

**This have not been released yet. Now developing prototype.**

## StreamSync

StreamSync is a google chrome extension that enables to sync playback position between Host and Client (different browsers).

## How StreamSync works

Host(chrome) -> (data) -> WS Server -> (data) -> Client(chrome)

Host can open a room and Client can join it.
Host send playback position data of video or streaming playing in browser to the room.
Clients in the same room receive the data and automatically sync the playback position of video or streaming. 

Host and Client have websocket connection with server as long as they are in the room.

## Architecture

![streamsync-architecture](https://user-images.githubusercontent.com/36187588/115284798-1a12dc80-a188-11eb-9105-2883d00cdc8d.png)

## Related Repository

**[atori74/streamsync](https://github.com/atori74/streamsync)**  
chrome extension as clientside application
