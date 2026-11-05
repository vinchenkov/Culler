package dispatch

import "time"

type Delivery struct {
	VersionID       int64
	CandidateID     int64
	CandidateMerged bool
	CaptureRun      string
	WaitUntil       *time.Time
	CorrectionOpen  bool
	// RecoveryRun and RecoveryOperation name the unfinished merge operation
	// whose execution ended. An unfinished apply precedes an unfinished prepare.
	RecoveryRun       string
	RecoveryOperation string
	// RecoveryStopPending: the owner of the RecoveryRun execution did not confirm that its host work stopped.
	RecoveryStopPending bool
	// ApplyPending: the task has an unfinished merge authorization.
	ApplyPending  bool
	ReviewPending bool
}

// priorityTask returns the approved task with the smallest task number. An
// operator approval gives its task the Project priority. A merge repair clears
// the approval, and the merge ends the priority.
func priorityTask(rec Records) (Task, bool) {
	var selected Task
	found := false
	for _, task := range rec.Tasks {
		if task.Archived || task.Decision != DecisionApproved {
			continue
		}
		if !found || task.ID < selected.ID {
			selected, found = task, true
		}
	}
	return selected, found
}

// mergeFocus holds the tasks whose merge steps the priority task admits. It
// is nil without a priority task, and empty while the priority task is
// blocked or stalled.
func mergeFocus(rec Records) map[int64]bool {
	task, ok := priorityTask(rec)
	switch {
	case !ok:
		return nil
	case task.Blocked || task.Stalled:
		return map[int64]bool{}
	}
	return map[int64]bool{task.ID: true}
}

func priorityAction(rec Records, task Task) Action {
	if task.Blocked || task.Stalled {
		return idle(IdleAttentionRequired)
	}
	if action, ok := focusAction(rec, task); ok {
		return action
	}
	return idle(IdleReason("priority-task-wait"))
}

// correctionAction selects the next agent run of an open correction. An open
// correction holds no Project priority. When two open corrections can start
// an agent run, the smaller task number starts first.
func correctionAction(rec Records) (Action, bool) {
	for _, task := range tasksByID(rec) {
		if !task.Delivery.CorrectionOpen || task.Archived || task.Blocked || task.Stalled ||
			task.Decision == DecisionCancelled || task.Decision == DecisionRejected {
			continue
		}
		if action, ok := focusAction(rec, task); ok {
			return action, true
		}
	}
	return Action{}, false
}

// focusAction selects the next agent run of the task.
func focusAction(rec Records, task Task) (Action, bool) {
	focused := rec
	focused.Tasks = []Task{task}
	if candidate, ok := nextDispatch(focused); ok {
		return spawnFor(focused, candidate), true
	}
	return Action{}, false
}

func reviewOccupancy(rec Records) int {
	count := 0
	for _, packet := range rec.Packets {
		if !packet.Archived && !packet.Superseded {
			count++
		}
	}
	return count
}
