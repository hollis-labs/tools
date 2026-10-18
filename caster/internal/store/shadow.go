package store

import (
	"database/sql"
	"errors"
	"time"
)

// Detection is one candidate seen in shadow mode. No message text is stored:
// only the ref, how it was found and what the policy dry-run decided.
type Detection struct {
	Extractor, Confidence, Ref, Scheme string
	State, Verdict, Error              string
	WouldShow                          bool
}

// Observation is one distinct text (by hash and extractor generation).
type Observation struct {
	ID                          int64
	Seen                        int
	Candidates, Resolved, Shown int
}

// FindObservation returns the observation for a text hash and generation and
// bumps its seen counter, or ErrNotFound.
func (s *Store) FindObservation(hash string, gen int) (Observation, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var o Observation
	err := s.db.QueryRow(`UPDATE observations SET seen=seen+1, last_at=? WHERE text_hash=? AND generation=?
	  RETURNING id, seen, candidates, resolved, would_show`, now, hash, gen).Scan(&o.ID, &o.Seen, &o.Candidates, &o.Resolved, &o.Shown)
	if errors.Is(err, sql.ErrNoRows) {
		return Observation{}, ErrNotFound
	}
	return o, err
}

// RecordObservation stores an observation and its detections atomically.
func (s *Store) RecordObservation(hash string, gen int, source string, dets []Detection) (Observation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	o := Observation{Seen: 1, Candidates: len(dets)}
	for _, d := range dets {
		if d.State == "resolved" {
			o.Resolved++
		}
		if d.WouldShow {
			o.Shown++
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`INSERT INTO observations(text_hash,generation,first_source,first_at,last_at,candidates,resolved,would_show) VALUES(?,?,?,?,?,?,?,?)`,
		hash, gen, source, now, now, o.Candidates, o.Resolved, o.Shown)
	if err != nil {
		return Observation{}, err
	}
	o.ID, _ = res.LastInsertId()
	for _, d := range dets {
		if _, err := tx.Exec(`INSERT INTO detections(observation_id,extractor,confidence,ref,scheme,state,verdict,would_show,error) VALUES(?,?,?,?,?,?,?,?,?)`,
			o.ID, d.Extractor, d.Confidence, d.Ref, d.Scheme, d.State, d.Verdict, d.WouldShow, d.Error); err != nil {
			return Observation{}, err
		}
	}
	return o, tx.Commit()
}

// ExtractorStats aggregates detections per extractor.
type ExtractorStats struct {
	Extractor, Confidence                    string
	Candidates, Resolved, Unresolved, Errors int
	WouldShow, LabeledTrue, LabeledFalse     int
	DistinctRefs                             int
}

// RefCount is a ref and how many detections named it.
type RefCount struct {
	Ref   string
	Count int
}

// ShadowTotals are the headline numbers.
type ShadowTotals struct {
	Observations, Repeats int
}

// ShadowReport summarizes shadow detections since a time (zero = all).
func (s *Store) ShadowReport(since time.Time, topN int) (ShadowTotals, []ExtractorStats, []RefCount, error) {
	cut := ""
	if !since.IsZero() {
		cut = since.UTC().Format(time.RFC3339Nano)
	}
	var tot ShadowTotals
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(seen-1),0) FROM observations WHERE last_at>=?`, cut).Scan(&tot.Observations, &tot.Repeats); err != nil {
		return tot, nil, nil, err
	}
	rows, err := s.db.Query(`SELECT d.extractor, d.confidence, COUNT(*),
	    SUM(d.state='resolved'), SUM(d.state='not_found' OR d.state='denied'), SUM(d.verdict='error'),
	    SUM(d.would_show), SUM(d.label='true'), SUM(d.label='false'), COUNT(DISTINCT d.ref)
	  FROM detections d JOIN observations o ON o.id=d.observation_id WHERE o.last_at>=? GROUP BY d.extractor, d.confidence ORDER BY d.confidence DESC, d.extractor`, cut)
	if err != nil {
		return tot, nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var stats []ExtractorStats
	for rows.Next() {
		var e ExtractorStats
		if err = rows.Scan(&e.Extractor, &e.Confidence, &e.Candidates, &e.Resolved, &e.Unresolved, &e.Errors, &e.WouldShow, &e.LabeledTrue, &e.LabeledFalse, &e.DistinctRefs); err != nil {
			return tot, nil, nil, err
		}
		stats = append(stats, e)
	}
	if err = rows.Err(); err != nil {
		return tot, nil, nil, err
	}
	rr, err := s.db.Query(`SELECT d.ref, COUNT(*) c FROM detections d JOIN observations o ON o.id=d.observation_id
	  WHERE o.last_at>=? AND d.state<>'resolved' GROUP BY d.ref ORDER BY c DESC, d.ref LIMIT ?`, cut, topN)
	if err != nil {
		return tot, nil, nil, err
	}
	defer func() { _ = rr.Close() }()
	var top []RefCount
	for rr.Next() {
		var c RefCount
		if err := rr.Scan(&c.Ref, &c.Count); err != nil {
			return tot, nil, nil, err
		}
		top = append(top, c)
	}
	return tot, stats, top, rr.Err()
}

// LabelRef records a human verdict ("true" = the detection was right, "false" =
// a false positive) on every detection of ref. It returns how many it touched.
func (s *Store) LabelRef(ref, verdict string) (int64, error) {
	res, err := s.db.Exec(`UPDATE detections SET label=? WHERE ref=?`, verdict, ref)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
