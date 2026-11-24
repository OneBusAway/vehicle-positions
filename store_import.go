package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// importStatus is what a CSV import does, or would do, with one row.
type importStatus string

const (
	importNew       importStatus = "new"
	importUpdate    importStatus = "update"
	importUnchanged importStatus = "unchanged"
	importError     importStatus = "error"
)

// importOutcome is one row's status. Changes lists in words what an update
// changes, and Note something the import leaves alone that the admin might
// expect it to change.
type importOutcome struct {
	Status  importStatus
	Reason  string // why the row can't be imported, when Status is importError
	Changes []string
	Note    string
	// ImportDrivers sets UserID on every driver row it imports, and Password
	// on the rows it created an account for.
	UserID   int64
	Password string
}

// vehicleImportRow is one validated row of a vehicles file.
type vehicleImportRow struct {
	Line      int
	ID        string
	Label     string
	AgencyTag string
}

// driverImportRow is one validated row of a drivers file, its email already
// lower-cased.
type driverImportRow struct {
	Line       int
	Email      string
	Name       string
	VehicleIDs []string
}

// CSVImporter plans and runs the admin site's CSV imports. A plan only reads.
// An import plans the rows again inside one transaction and writes all of
// them, or nothing when any row has an error, and it imports a preview only
// once.
type CSVImporter interface {
	PlanVehicleImport(ctx context.Context, rows []vehicleImportRow) ([]importOutcome, error)
	ImportVehicles(ctx context.Context, preview importPreview, rows []vehicleImportRow) ([]importOutcome, error)
	PlanDriverImport(ctx context.Context, rows []driverImportRow) ([]importOutcome, error)
	ImportDrivers(ctx context.Context, preview importPreview, rows []driverImportRow, newPassword func() string) ([]importOutcome, error)
}

// importPreview is the confirmed preview an import comes from: the signature
// that makes it unique, and when it stops being confirmable.
type importPreview struct {
	Signature []byte
	Expires   time.Time
}

// ErrPreviewImported is returned for a preview that was already imported.
// Without it, an admin could confirm a preview again within its hour and, for
// example, bring back a driver deleted since the first import.
var ErrPreviewImported = errors.New("preview already imported")

// claimPreview records preview as imported, in the import's transaction: a
// second confirm finds the record and imports nothing, and a confirm that
// fails rolls the record back, leaving the preview usable. A record is
// dropped an hour after its preview expired, which leaves room for clocks
// that differ between servers.
func claimPreview(ctx context.Context, q *db.Queries, preview importPreview) error {
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-importPreviewLifetime), Valid: true}
	if err := q.DeleteImportConfirmationsBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("delete old import confirmations: %w", err)
	}
	n, err := q.RecordImportConfirmation(ctx, db.RecordImportConfirmationParams{
		Signature: preview.Signature,
		ExpiresAt: pgtype.Timestamptz{Time: preview.Expires, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("record import confirmation: %w", err)
	}
	if n == 0 {
		return ErrPreviewImported
	}
	return nil
}

var _ CSVImporter = (*Store)(nil)

func importFailed(outcomes []importOutcome) bool {
	return slices.ContainsFunc(outcomes, func(o importOutcome) bool { return o.Status == importError })
}

// PlanVehicleImport says what ImportVehicles would do with each row.
func (s *Store) PlanVehicleImport(ctx context.Context, rows []vehicleImportRow) ([]importOutcome, error) {
	return planVehicles(ctx, s.queries, rows)
}

// planVehicles finds each row's vehicle: an unknown id is new, and a known one
// is an update when its label or agency tag differs. A vehicle's active flag
// is never changed, as on the vehicle edit form.
func planVehicles(ctx context.Context, q *db.Queries, rows []vehicleImportRow) ([]importOutcome, error) {
	outcomes := make([]importOutcome, len(rows))
	for i, row := range rows {
		v, err := q.GetVehicleByID(ctx, row.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			outcomes[i] = importOutcome{Status: importNew}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get vehicle %q: %w", row.ID, err)
		}
		o := importOutcome{Status: importUnchanged}
		if v.Label != row.Label {
			o.Changes = append(o.Changes, "label was "+wasValue(v.Label))
		}
		if v.AgencyTag != row.AgencyTag {
			o.Changes = append(o.Changes, "agency tag was "+wasValue(v.AgencyTag))
		}
		if len(o.Changes) > 0 {
			o.Status = importUpdate
		}
		if !v.Active {
			o.Note = "stays deactivated"
		}
		outcomes[i] = o
	}
	return outcomes, nil
}

func wasValue(v string) string {
	if v == "" {
		return "empty"
	}
	return v
}

// ImportVehicles creates the new vehicles and updates the changed ones in one
// transaction. Every row was validated before the call, and a vehicle row has
// no reason to fail that only the database knows, so a row error here means
// another admin changed the same vehicle meanwhile, and nothing is written.
//
// The vehicles are written in id order, not the file's, so two imports of the
// same vehicles queue behind each other instead of deadlocking. A driver import
// locks its vehicles in that order too (vehicleProblems).
func (s *Store) ImportVehicles(ctx context.Context, preview importPreview, rows []vehicleImportRow) ([]importOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	q := s.queries.WithTx(tx)

	if err := claimPreview(ctx, q, preview); err != nil {
		return nil, err
	}
	outcomes, err := planVehicles(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(rows[a].ID, rows[b].ID) })
	for _, i := range order {
		row := rows[i]
		switch outcomes[i].Status {
		case importNew:
			// createVehicle and updateVehicleInfo are the vehicle forms'
			// writes, audit row and all; neither touches the active flag.
			created, err := createVehicle(ctx, q, row.ID, row.Label, row.AgencyTag)
			if err != nil {
				return nil, fmt.Errorf("create vehicle %q: %w", row.ID, err)
			}
			if !created {
				outcomes[i] = importOutcome{Status: importError, Reason: "vehicle id already exists"}
				return outcomes, nil
			}
		case importUpdate:
			err := updateVehicleInfo(ctx, q, row.ID, row.Label, row.AgencyTag)
			if errors.Is(err, pgx.ErrNoRows) {
				outcomes[i] = importOutcome{Status: importError, Reason: "vehicle not found"}
				return outcomes, nil
			}
			if err != nil {
				return nil, fmt.Errorf("update vehicle %q: %w", row.ID, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return outcomes, nil
}

// driverPlan is what applying one planned driver row needs: the existing
// account (userID 0 for a new one), and the vehicles to assign.
type driverPlan struct {
	userID int64
	rename bool
	assign []string
}

// PlanDriverImport says what ImportDrivers would do with each row.
func (s *Store) PlanDriverImport(ctx context.Context, rows []driverImportRow) ([]importOutcome, error) {
	outcomes, _, err := planDrivers(ctx, s.queries, rows)
	return outcomes, err
}

// planDrivers matches each row to an account by email in any case. A row with
// no account is new. A driver's account is an update when the name differs or
// a listed vehicle isn't assigned yet: an import only adds assignments, and
// never changes an email, a password, a role or the active flag. An admin's
// or a viewer's account is an error, so the import can't touch one.
func planDrivers(ctx context.Context, q *db.Queries, rows []driverImportRow) ([]importOutcome, []driverPlan, error) {
	outcomes := make([]importOutcome, len(rows))
	plans := make([]driverPlan, len(rows))
	for i, row := range rows {
		o := importOutcome{Status: importNew}
		p := driverPlan{assign: row.VehicleIDs}
		u, err := q.GetUserByEmail(ctx, row.Email)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, nil, fmt.Errorf("find account for line %d: %w", row.Line, err)
		case u.Role == roleViewer:
			outcomes[i] = importOutcome{Status: importError, Reason: "this email belongs to a viewer account"}
			continue
		case u.Role != roleDriver:
			outcomes[i] = importOutcome{Status: importError, Reason: "this email belongs to an admin account"}
			continue
		default:
			assigned, err := q.ListVehiclesByUser(ctx, u.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("list assignments of user %d: %w", u.ID, err)
			}
			p = driverPlan{userID: u.ID, rename: u.Name != row.Name}
			for _, id := range row.VehicleIDs {
				if !slices.ContainsFunc(assigned, func(a db.UserVehicle) bool { return a.VehicleID == id }) {
					p.assign = append(p.assign, id)
				}
			}
			o.Status = importUnchanged
			if p.rename {
				o.Changes = append(o.Changes, "name was "+u.Name)
			}
			if len(p.assign) > 0 {
				o.Changes = append(o.Changes, "adds "+strings.Join(p.assign, ", "))
			}
			if len(o.Changes) > 0 {
				o.Status = importUpdate
			}
			if !u.Active {
				o.Note = "stays deactivated"
			}
		}

		outcomes[i] = o
		plans[i] = p
	}

	problems, err := vehicleProblems(ctx, q, plans)
	if err != nil {
		return nil, nil, err
	}
	for i, p := range plans {
		var reasons []string
		for _, id := range p.assign {
			if reason, ok := problems[id]; ok {
				reasons = append(reasons, reason)
			}
		}
		if len(reasons) > 0 {
			outcomes[i] = importOutcome{Status: importError, Reason: strings.Join(reasons, "; ")}
		}
	}
	return outcomes, plans, nil
}

// vehicleProblems is why the vehicles plans assign can't be assigned, by id,
// with nothing for one that can. Drivers who share a pool of vehicles list the
// same ones on every row, so it asks about each vehicle once, not once a row.
// In the import's transaction checkAssignableVehicle's lock keeps each one
// active until the import commits.
func vehicleProblems(ctx context.Context, q *db.Queries, plans []driverPlan) (map[string]string, error) {
	var ids []string
	for _, p := range plans {
		ids = append(ids, p.assign...)
	}
	slices.Sort(ids)
	problems := make(map[string]string)
	for _, id := range slices.Compact(ids) {
		err := checkAssignableVehicle(ctx, q, id)
		if err == nil {
			continue
		}
		reason, ok := assignmentProblem(id, err)
		if !ok {
			return nil, err
		}
		problems[id] = reason
	}
	return problems, nil
}

// assignmentProblem words a refused assignment for the row that asked for it.
// ok is false for an error that isn't the row's fault.
func assignmentProblem(vehicleID string, err error) (reason string, ok bool) {
	switch {
	case errors.Is(err, ErrVehicleNotFoundFK):
		return "vehicle " + vehicleID + " not found", true
	case errors.Is(err, ErrVehicleInactive):
		return "vehicle " + vehicleID + " is inactive", true
	case errors.Is(err, ErrAssignmentExists):
		// Another admin assigned it after the plan read the assignments.
		return "vehicle " + vehicleID + " was assigned meanwhile; preview the file again", true
	}
	return "", false
}

// ImportDrivers creates the new drivers' accounts and updates the changed
// ones in one transaction, or writes nothing when any row has an error. Each
// new account gets a password from newPassword, returned once in its row's
// outcome.
//
// Hashing a password takes about a tenth of a second, so a first plan,
// outside the transaction, says which rows need one, and they are hashed in
// parallel before the transaction opens. The rows are planned again inside
// it; one that turned new since then has no password and fails the import.
func (s *Store) ImportDrivers(ctx context.Context, preview importPreview, rows []driverImportRow, newPassword func() string) ([]importOutcome, error) {
	outcomes, _, err := planDrivers(ctx, s.queries, rows)
	if err != nil {
		return nil, err
	}
	if importFailed(outcomes) {
		return outcomes, nil
	}
	passwords := make(map[int]string)
	for i, o := range outcomes {
		if o.Status == importNew {
			passwords[i] = newPassword()
		}
	}
	hashes, err := hashPasswords(ctx, passwords)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	q := s.queries.WithTx(tx)

	if err := claimPreview(ctx, q, preview); err != nil {
		return nil, err
	}
	outcomes, plans, err := planDrivers(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	if importFailed(outcomes) {
		return outcomes, nil
	}
	locked, err := lockUpdatedAccounts(ctx, q, outcomes, plans)
	if err != nil {
		return nil, err
	}
	if importFailed(outcomes) {
		return outcomes, nil
	}
	// The accounts are created in email order, not the file's, so two imports
	// that create the same drivers queue behind each other instead of
	// deadlocking on their emails.
	creates := make([]int, 0, len(passwords))
	for i, o := range outcomes {
		if o.Status == importNew {
			creates = append(creates, i)
		}
	}
	slices.SortFunc(creates, func(a, b int) int { return cmp.Compare(rows[a].Email, rows[b].Email) })
	for _, i := range creates {
		o, row := &outcomes[i], rows[i]
		hash, ok := hashes[i]
		if !ok {
			*o = importOutcome{Status: importError, Reason: "an account with this email was removed meanwhile; preview the file again"}
			return outcomes, nil
		}
		u, err := createUser(ctx, q, db.CreateUserParams{Name: row.Name, Email: row.Email, PasswordHash: string(hash), Role: roleDriver})
		if err != nil {
			if isDuplicateEmail(err) {
				*o = importOutcome{Status: importError, Reason: "email already exists"}
				return outcomes, nil
			}
			return nil, fmt.Errorf("create user for line %d: %w", row.Line, err)
		}
		o.UserID, o.Password = u.ID, passwords[i]
	}
	for i, row := range rows {
		o, p := &outcomes[i], plans[i]
		switch o.Status {
		case importUnchanged:
			o.UserID = p.userID
			continue
		case importUpdate:
			if p.rename {
				cur := locked[i]
				if _, err := updateUser(ctx, q, p.userID, row.Name, cur.Email, cur.Role); err != nil {
					return nil, fmt.Errorf("update user %d: %w", p.userID, err)
				}
			}
			o.UserID = p.userID
		}
		for _, id := range p.assign {
			// Not assignVehicle: the plan above checked the vehicle, and the
			// account is a driver's, new in this transaction or locked by it.
			if _, err := insertAssignment(ctx, q, o.UserID, id); err != nil {
				reason, ok := assignmentProblem(id, err)
				if !ok {
					return nil, fmt.Errorf("assign vehicle %q to user %d: %w", id, o.UserID, err)
				}
				*o = importOutcome{Status: importError, Reason: reason}
				return outcomes, nil
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return outcomes, nil
}

// lockUpdatedAccounts reads again, under the lock updateUser takes anyway, the
// account of every row that updates one, before anything is written, and
// returns them indexed by row. The plans read the accounts without a lock, so
// another admin may have changed one since. updateUser writes back the email
// and role it is given, so a rename passes on the ones read here and changes
// only the name. An account that is no longer a driver's, or is gone, is left
// alone, as the plan leaves one: its row's outcome becomes the error, and
// nothing more is read.
//
// The accounts are locked in id order, not the file's, so two imports that
// update the same drivers queue behind each other instead of deadlocking.
func lockUpdatedAccounts(ctx context.Context, q *db.Queries, outcomes []importOutcome, plans []driverPlan) ([]db.GetUserForUpdateRow, error) {
	updates := make([]int, 0, len(outcomes))
	for i, o := range outcomes {
		if o.Status == importUpdate {
			updates = append(updates, i)
		}
	}
	slices.SortFunc(updates, func(a, b int) int { return cmp.Compare(plans[a].userID, plans[b].userID) })

	locked := make([]db.GetUserForUpdateRow, len(outcomes))
	for _, i := range updates {
		cur, err := q.GetUserForUpdate(ctx, plans[i].userID)
		if errors.Is(err, pgx.ErrNoRows) {
			outcomes[i] = importOutcome{Status: importError, Reason: "an account with this email was removed meanwhile; preview the file again"}
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("lock user %d: %w", plans[i].userID, err)
		}
		if cur.Role != roleDriver {
			reason := "this account was made an admin meanwhile; preview the file again"
			if cur.Role == roleViewer {
				reason = "this account was made a viewer meanwhile; preview the file again"
			}
			outcomes[i] = importOutcome{Status: importError, Reason: reason}
			return nil, nil
		}
		locked[i] = cur
	}
	return locked, nil
}

// hashPasswords bcrypt-hashes each password at the cost every other password
// gets, one per core at a time, and stops early when ctx ends.
func hashPasswords(ctx context.Context, passwords map[int]string) (map[int][]byte, error) {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		hashes   = make(map[int][]byte, len(passwords))
		firstErr error
		slots    = make(chan struct{}, runtime.GOMAXPROCS(0))
	)
	for i, password := range passwords {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			if ctx.Err() != nil {
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			hashes[i] = hash
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, fmt.Errorf("hash password: %w", firstErr)
	}
	return hashes, nil
}
