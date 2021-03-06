/*
 Copyright (C) 2013 Aaron Lindsay <aaron@aclindsay.com>
*/

package main

import (
	"github.com/aclindsa/asink"
)

type pathMapRequest struct {
	path         string
	local        bool /*is this event local (true) or remote(false)?*/
	responseChan chan *asink.Event
}

type pathMapValue struct {
	latestEvent   *asink.Event
	locked        bool
	localWaiters  []chan *asink.Event
	remoteWaiters []chan *asink.Event
}

var pathLockerChan = make(chan *pathMapRequest)
var pathUnlockerChan = make(chan *asink.Event)

func PathLocker(db *AsinkDB) {
	var event *asink.Event
	var request *pathMapRequest
	var v *pathMapValue
	var ok bool
	var c chan *asink.Event
	m := make(map[string]*pathMapValue)

	for {
		select {
		case event = <-pathUnlockerChan:
			if v, ok = m[event.Path]; ok != false {
				//only update status in data structures if the event hasn't been discarded
				if event.LocalStatus&asink.DISCARDED == 0 && event.LocalStatus&asink.NOSAVE == 0 {
					if v.latestEvent == nil || !v.latestEvent.IsSameEvent(event) {
						err := db.DatabaseAddEvent(event)
						if err != nil {
							panic(err)
						}
						//TODO batch database writes instead of doing one at a time
					} else if v.latestEvent.Id == 0 {
						//can only get here if latestEvent exists and is the same for 'event'
						//except for Id, so update latestEvent's Id in the database.
						v.latestEvent.Id = event.Id
						event = v.latestEvent
						err := db.DatabaseUpdateEvent(event)
						if err != nil {
							panic(err)
						}
					}
					v.latestEvent = event
				}
				if len(v.localWaiters) > 0 {
					c = v.localWaiters[0]
					v.localWaiters = v.localWaiters[1:]
					c <- v.latestEvent
				} else if len(v.remoteWaiters) > 0 {
					c = v.remoteWaiters[0]
					v.remoteWaiters = v.remoteWaiters[1:]
					c <- v.latestEvent
				} else {
					v.locked = false
				}
			}
		case request = <-pathLockerChan:
			v, ok = m[request.path]
			//allocate pathMapValue object if it doesn't exist
			if !ok {
				v = new(pathMapValue)
				m[request.path] = v
			}
			if v.locked {
				if request.local {
					v.localWaiters = append(v.localWaiters, request.responseChan)
				} else {
					v.remoteWaiters = append(v.remoteWaiters, request.responseChan)
				}
			} else {
				v.locked = true
				event, err := db.DatabaseLatestEventForPath(request.path)
				if err != nil {
					panic(err)
				}
				request.responseChan <- event
			}
		}
	}
}

//locks the path to ensure nothing else inside asink is mucking with that file.
//'local' determines the precedence of the lock - all local lock requesters will
//be served before any remote requesters.
//The previous event for this path is returned, nil is returned if no previous event exists
func LockPath(path string, local bool) (currentEvent *asink.Event) {
	c := make(chan *asink.Event)
	pathLockerChan <- &pathMapRequest{path, local, c}
	return <-c
}

//unlocks the path, storing the updated event back to the database
func UnlockPath(event *asink.Event) {
	pathUnlockerChan <- event
}
