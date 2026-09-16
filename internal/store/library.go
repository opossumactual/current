package store

type Library struct {
	Feeds      int `json:"feeds"`
	Categories int `json:"categories"`
	Articles   int `json:"articles"`
	Images     int `json:"images"`
	Paused     int `json:"paused"`
	Archived   int `json:"archived"`
}

func (s *Store) Library() (*Library, error) {
	var v Library
	err := s.db.QueryRow(`SELECT
 (SELECT count(*) FROM feeds WHERE unsubscribed=0),
 (SELECT count(*) FROM categories), (SELECT count(*) FROM articles),
 (SELECT count(*) FROM articles WHERE image_url<>''),
 (SELECT count(*) FROM feeds WHERE paused=1 AND unsubscribed=0),
 (SELECT count(*) FROM feeds WHERE unsubscribed=1)`).Scan(&v.Feeds, &v.Categories, &v.Articles, &v.Images, &v.Paused, &v.Archived)
	return &v, err
}
