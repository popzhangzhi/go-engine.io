package payload

import (
	"fmt"
	"sync"
)

type pauserStatus int

const (
	statusNormal pauserStatus = iota
	statusPausing
	statusPaused
)

type pauser struct {
	l       sync.Mutex
	c       *sync.Cond
	worker  int
	pausing chan struct{}
	paused  chan struct{}
	status  pauserStatus
}

func newPauser() *pauser {
	ret := &pauser{
		pausing: make(chan struct{}),
		paused:  make(chan struct{}),
		status:  statusNormal,
	}
	ret.c = sync.NewCond(&ret.l)
	return ret
}

func (p *pauser) Pause() bool {
	p.l.Lock()
	defer p.l.Unlock()
	fmt.Println(p.status)
	// 判断当前暂停的状态
	// 第一次由 statusNormal->statusPausing worker =0 不暂停，状态变成statusPaused
	// 第二次 statusPaused 所以return false
	switch p.status {
	case statusPaused:
		return false
	case statusNormal:
		close(p.pausing)
		p.status = statusPausing
	}
	fmt.Println("p.worker", p.worker)
	// 当worker大于0的时候，会暂停该goroutine
	for p.worker != 0 {
		p.c.Wait()
	}

	if p.status == statusPaused {
		return false
	}
	close(p.paused)
	p.status = statusPaused
	p.c.Broadcast()

	return true
}

func (p *pauser) Resume() {
	p.l.Lock()
	defer p.l.Unlock()
	p.status = statusNormal
	p.paused = make(chan struct{})
	p.pausing = make(chan struct{})
}

func (p *pauser) Working() bool {
	p.l.Lock()
	defer p.l.Unlock()
	if p.status == statusPaused {
		return false
	}
	p.worker++
	return true
}

func (p *pauser) Done() {
	p.l.Lock()
	defer p.l.Unlock()
	if p.status == statusPaused || p.worker == 0 {
		return
	}
	p.worker--
	p.c.Broadcast()
}

func (p *pauser) PausingTrigger() <-chan struct{} {
	p.l.Lock()
	defer p.l.Unlock()
	return p.pausing
}

func (p *pauser) PausedTrigger() <-chan struct{} {
	p.l.Lock()
	defer p.l.Unlock()
	return p.paused
}
