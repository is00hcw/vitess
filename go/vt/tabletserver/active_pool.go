// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tabletserver

import (
	"fmt"
	"time"

	log "github.com/golang/glog"
	"github.com/youtube/vitess/go/pools"
	"github.com/youtube/vitess/go/stats"
	"github.com/youtube/vitess/go/sync2"
	"github.com/youtube/vitess/go/timer"
)

type ActivePool struct {
	pool     *pools.Numbered
	timeout  sync2.AtomicDuration
	connPool *ConnectionPool
	ticks    *timer.Timer
}

func NewActivePool(name string, queryTimeout, idleTimeout time.Duration) *ActivePool {
	ap := &ActivePool{
		pool:     pools.NewNumbered(),
		timeout:  sync2.AtomicDuration(queryTimeout),
		connPool: NewConnectionPool("", 1, idleTimeout),
		ticks:    timer.NewTimer(queryTimeout / 10),
	}
	stats.Publish(name+"Size", stats.IntFunc(ap.pool.Size))
	stats.Publish(
		name+"Timeout",
		stats.DurationFunc(func() time.Duration { return ap.timeout.Get() }),
	)
	return ap
}

func (ap *ActivePool) Open(ConnFactory CreateConnectionFunc) {
	ap.connPool.Open(ConnFactory)
	ap.ticks.Start(func() { ap.QueryKiller() })
}

func (ap *ActivePool) Close() {
	ap.ticks.Stop()
	ap.connPool.Close()
	ap.pool = pools.NewNumbered()
}

func (ap *ActivePool) QueryKiller() {
	defer logError()
	for _, v := range ap.pool.GetOutdated(time.Duration(ap.Timeout()), "for abort") {
		ap.kill(v.(int64))
	}
}

func (ap *ActivePool) kill(connid int64) {
	ap.Remove(connid)
	killStats.Add("Queries", 1)
	log.Infof("killing query %d", connid)
	killConn := ap.connPool.Get()
	defer killConn.Recycle()
	sql := fmt.Sprintf("kill %d", connid)
	if _, err := killConn.ExecuteFetch(sql, 10000, false); err != nil {
		log.Errorf("Could not kill query %d: %v", connid, err)
	}
}

func (ap *ActivePool) Put(id int64) {
	ap.pool.Register(id, id)
}

func (ap *ActivePool) Remove(id int64) {
	ap.pool.Unregister(id)
}

func (ap *ActivePool) Timeout() time.Duration {
	return ap.timeout.Get()
}

func (ap *ActivePool) SetTimeout(timeout time.Duration) {
	ap.timeout.Set(timeout)
	ap.ticks.SetInterval(timeout / 10)
}

func (ap *ActivePool) SetIdleTimeout(idleTimeout time.Duration) {
	ap.connPool.SetIdleTimeout(idleTimeout)
}

func (ap *ActivePool) StatsJSON() string {
	s, t := ap.Stats()
	return fmt.Sprintf("{\"Size\": %v, \"Timeout\": %v}", s, int64(t))
}

func (ap *ActivePool) Stats() (size int64, timeout time.Duration) {
	return ap.pool.Size(), ap.Timeout()
}
