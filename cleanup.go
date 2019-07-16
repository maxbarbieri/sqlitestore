package sqlitestore

import (
	"log"
	"time"

	"github.com/gorilla/sessions"
)

var defaultInterval = time.Minute * 5

// StartCleanup runs a background goroutine every interval that deletes expired sessions from the database.
// The design is based on https://github.com/nwmac/sqlitestore

func (m *SqliteStore) StartCleanup(sessionName string, interval time.Duration) (chan<- struct{}, <-chan struct{}) {
	if interval <= 0 {
		interval = defaultInterval
	}

	quit, done := make(chan struct{}), make(chan struct{})
	go m.cleanup(sessionName, interval, quit, done)
	return quit, done
}

// cleanup deletes expired sessions at set intervals.
func (m *SqliteStore) cleanup(sessionName string, interval time.Duration, quit <-chan struct{}, done chan<- struct{}) {
	ticker := time.NewTicker(interval)

	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case <-quit:
			// Handle the quit signal.
			done <- struct{}{}
			return
		case <-ticker.C:
			// Delete expired sessions on each tick.
			err := m.deleteExpiredSessions(sessionName)
			if err != nil {
				log.Println("Unable to delete expired sessions: ", err.Error())
			}
		}
	}
}

//gets the IDs of all the expired sessions, in the meantime it calls the callback for each one of them, if it has been set
func (m *SqliteStore) getExpiredSessionsIdsAndCallCallbacks(sessionName string) ([]string, error) {
	//select IDs of all expired sessions
	expiredSessionsSelectStmt, err := m.db.Prepare("SELECT id FROM " + m.table + " WHERE expires_on < datetime(CURRENT_TIMESTAMP,'localtime')")
	if err != nil {
		log.Println("Error preparing select statement:", err.Error())
		return nil, err
	}
	defer expiredSessionsSelectStmt.Close()
	expiredSessionsRows, err := expiredSessionsSelectStmt.Query()
	if err != nil {
		log.Println("Error executing select query:", err.Error())
		return nil, err
	}
	defer expiredSessionsRows.Close()

	var expiredSessionsIds []string
	var expiredSessionId string
	for {
		if !expiredSessionsRows.Next() {
			break
		}
		err = expiredSessionsRows.Scan(&expiredSessionId)
		if err != nil {
			log.Println("Error scanning select query result:", err.Error())
			continue //go to the next session id
		}

		//append the session id to the slice, so it can be accessed later
		expiredSessionsIds = append(expiredSessionsIds, expiredSessionId)

		//load the session from the database
		session := sessions.NewSession(m, sessionName)
		session.ID = expiredSessionId
		session.Options = &sessions.Options{
			Path:     m.Options.Path,
			MaxAge:   m.Options.MaxAge,
			HttpOnly: m.Options.HttpOnly,
			Secure:   m.Options.Secure,
			Domain:   m.Options.Domain,
			SameSite: m.Options.SameSite,
		}
		err := m.load(session, true) //true flag to ignore the check for expired session
		if err != nil {
			log.Println("Error loading (expired) session:", err.Error())
			continue //go to the next session id
		}

		//call the callback for this session
		if m.expiredSessionPreDeleteCallback != nil {
			m.expiredSessionPreDeleteCallback(session)
		}
	}

	return expiredSessionsIds, nil
}

// deletes the expired sessions
func (m *SqliteStore) deleteExpiredSessions(sessionName string) error {
	expiredSessionsIds, err := m.getExpiredSessionsIdsAndCallCallbacks(sessionName)
	if err != nil {
		return err
	}

	for i := 0; i < len(expiredSessionsIds); i++ {
		//delete the session from the database
		_, delErr := m.stmtDelete.Exec(expiredSessionsIds[i])
		if delErr != nil {
			return delErr
		}
	}

	return nil
}

// StopCleanup stops the background cleanup from running.
func (m *SqliteStore) StopCleanup(quit chan<- struct{}, done <-chan struct{}) {
	quit <- struct{}{}
	<-done
}

func (m *SqliteStore) SetExpiredSessionPreDeleteCallback(callback func(*sessions.Session)) {
	m.expiredSessionPreDeleteCallback = callback
}
