package server

import "log"

// TriggerNewRoom allows the intent callback to directly trigger a room switch.
func (rm *RoomManager) TriggerNewRoom(roomURL, keyHex string) {
	if rm.onNewRoom != nil {
		log.Printf("[ROOM-MGR] TriggerNewRoom: %s", roomURL)
		rm.onNewRoom(roomURL, keyHex)
	} else {
		log.Printf("[ROOM-MGR] TriggerNewRoom: no callback set")
	}
}
