package store

import "context"

// OwnerAuthorID returns the single distinct author_id on authored rows.
// Import-only archives have no config user_id, so this cannot use config.
// Two or more distinct ids disables "me" rendering.
func (s *Store) OwnerAuthorID(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
select distinct t.author_id
from tweets t
join tweet_roles r on r.tweet_id = t.id and r.role = 'authored'
where trim(coalesce(t.author_id, '')) <> ''
order by t.author_id
limit 2`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", nil
	}
	return ids[0], nil
}
