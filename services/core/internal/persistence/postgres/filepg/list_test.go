package filepg_test

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

func TestFileListPaginationIsolationAndReconnect(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool)
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := store.List(t.Context(), tenant, files.ListQuery{Limit: files.MaxPageSize})
	if err != nil || empty.Files == nil || len(empty.Files) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}

	all := make([]files.File, 0, 105)
	for i := range 105 {
		file := create(t, service, t.Context(), tenant, []byte{byte(i)})
		stamp := time.Unix(1700000000+int64(i%2), 0).UTC()
		if _, err := pool.Exec(t.Context(), "UPDATE source_files SET created_at=$1 WHERE tenant_id=$2 AND id=$3", stamp, tenant, strings.TrimPrefix(file.ID, "file-")); err != nil {
			t.Fatal(err)
		}
		file, err = store.Get(t.Context(), tenant, file.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, file)
	}
	slices.SortFunc(all, func(a, b files.File) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	foreign := create(t, service, t.Context(), other, []byte("foreign"))

	read := func(current *filepg.Store, ascending bool, purpose *string, size int) []files.File {
		t.Helper()
		actual := []files.File{}
		cursor := ""
		for {
			page, err := current.List(t.Context(), tenant, files.ListQuery{After: cursor, Limit: size, Ascending: ascending, Purpose: purpose})
			if err != nil || len(page.Files) == 0 || len(page.Files) > size {
				t.Fatalf("page: %+v, %v", page, err)
			}
			actual = append(actual, page.Files...)
			if len(actual) > len(all) {
				t.Fatal("pagination repeated files")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Files[len(page.Files)-1].ID {
				t.Fatal("cursor is not the last included file")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	userData, otherPurpose, badPurpose := files.PurposeUserData, "batch", "bad\x00purpose"
	for _, ascending := range []bool{true, false} {
		want := slices.Clone(all)
		if !ascending {
			slices.Reverse(want)
		}
		for _, purpose := range []*string{nil, &userData} {
			for _, size := range []int{17, files.MaxPageSize} {
				if got := read(store, ascending, purpose, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("ordered page mismatch: ascending=%t size=%d", ascending, size)
				}
			}
		}
	}
	filtered, err := store.List(t.Context(), tenant, files.ListQuery{Limit: files.MaxPageSize, Purpose: &otherPurpose})
	if err != nil || filtered.Files == nil || len(filtered.Files) != 0 || filtered.NextCursor != "" {
		t.Fatalf("purpose filter: %+v, %v", filtered, err)
	}
	for _, cursor := range []string{foreign.ID, "file-" + uuid.NewString(), "invalid"} {
		if _, err := store.List(t.Context(), tenant, files.ListQuery{After: cursor, Limit: 20, Ascending: true}); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("foreign/unknown cursor %q: %v", cursor, err)
		}
	}
	for _, tc := range []struct {
		tenant string
		query  files.ListQuery
	}{
		{"invalid", files.ListQuery{Limit: 20}}, {tenant, files.ListQuery{}}, {tenant, files.ListQuery{Limit: files.MaxPageSize + 1}},
		{tenant, files.ListQuery{Limit: 20, Purpose: &badPurpose}},
	} {
		if _, err := store.List(t.Context(), tc.tenant, tc.query); !errors.Is(err, files.ErrInvalidInput) {
			t.Fatalf("invalid query %+v: %v", tc.query, err)
		}
	}
	tail, err := store.List(t.Context(), tenant, files.ListQuery{After: all[len(all)-1].ID, Limit: files.MaxPageSize, Ascending: true})
	if err != nil || tail.Files == nil || len(tail.Files) != 0 || tail.NextCursor != "" {
		t.Fatalf("terminal page: %+v, %v", tail, err)
	}
	foreignPage, err := store.List(t.Context(), other, files.ListQuery{Limit: files.MaxPageSize})
	if err != nil || !reflect.DeepEqual(foreignPage.Files, []files.File{foreign}) || foreignPage.NextCursor != "" {
		t.Fatalf("project isolation: %+v, %v", foreignPage, err)
	}
	deleted := create(t, service, t.Context(), tenant, nil)
	if err := service.Delete(t.Context(), files.DeleteCommand{TenantID: tenant, FileID: deleted.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(t.Context(), tenant, files.ListQuery{After: deleted.ID, Limit: 20}); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("deleted cursor accepted: %v", err)
	}

	pool.Close()
	reopened, _ := open(t, pgtest.Open(t))
	if got := read(reopened, true, nil, 17); !reflect.DeepEqual(got, all) {
		t.Fatal("listing changed after reconnect")
	}
}
