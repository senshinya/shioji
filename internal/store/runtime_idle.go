package store

// RuntimeIdleState is a read-only, session-scoped admission snapshot. Both
// revisions matter: a queue can change without changing the session revision.
type RuntimeIdleState struct {
	Revision, QueueRevision int64
	Idle                    bool
	Background              bool
}

func (s *Store) RuntimeIdle(id string) (RuntimeIdleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out RuntimeIdleState
	err := s.db.QueryRow(runtimeIdleQuery, id, false).Scan(&out.Revision, &out.QueueRevision, &out.Idle)
	return out, err
}

const runtimeIdleQuery = `SELECT json_extract(s.body,'$.revision'),
 COALESCE((SELECT json_extract(body,'$.revision') FROM queues WHERE session_id=s.id),0),
 COALESCE(json_extract(s.body,'$.status'),'') IN ('idle','completed','interrupted','failed','abandoned')
 AND COALESCE(json_extract(s.body,'$.executionGuard'),'')=''
 AND COALESCE(json_extract(s.body,'$.contextAction.state'),'') IN ('','completed','interrupted','failed')
 AND NOT EXISTS(SELECT 1 FROM operations WHERE session_id=s.id AND state IN ('accepted','dispatched','indeterminate'))
 AND NOT EXISTS(SELECT 1 FROM queues WHERE session_id=s.id AND json_array_length(body,'$.items')>0)
 AND NOT EXISTS(SELECT 1 FROM requests WHERE session_id=s.id AND json_extract(body,'$.state') NOT IN ('answered','resolved','invalidated'))
 AND NOT EXISTS(SELECT 1 FROM turns WHERE session_id=s.id AND json_extract(body,'$.status') NOT IN ('completed','interrupted','failed','abandoned'))
 AND NOT EXISTS(SELECT 1 FROM works WHERE session_id=s.id AND COALESCE(json_extract(body,'$.state'),'') NOT IN ('completed','stopped','failed') AND (COALESCE(json_extract(body,'$.originRef'),'')='' OR json_extract(body,'$.originRef')=json_extract(s.body,'$.threadId')) AND NOT (?2 AND json_extract(body,'$.kind')='background' AND json_extract(body,'$.state')='waiting'))
 AND NOT EXISTS(SELECT 1 FROM work_inputs WHERE session_id=s.id AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0)
 AND NOT EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked') AND json_extract(body,'$.source.id')=?1)
 AND NOT EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked') AND json_extract(body,'$.session.id')=?1)
 FROM sessions s WHERE s.id=?1`

// RuntimeGCIdle distinguishes waiting native background work from a running
// agent/tool. Ordinary admission and archive guards continue to use RuntimeIdle.
func (s *Store) RuntimeGCIdle(id string) (RuntimeIdleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out RuntimeIdleState
	err := s.db.QueryRow(runtimeIdleQuery, id, true).Scan(&out.Revision, &out.QueueRevision, &out.Idle)
	if err == nil {
		err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM works w JOIN sessions s ON w.session_id=s.id WHERE s.id=? AND json_extract(w.body,'$.kind')='background' AND json_extract(w.body,'$.state')='waiting' AND (COALESCE(json_extract(w.body,'$.originRef'),'')='' OR json_extract(w.body,'$.originRef')=json_extract(s.body,'$.threadId')))`, id).Scan(&out.Background)
	}
	return out, err
}
