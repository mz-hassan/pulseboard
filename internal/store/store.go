package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrRoomNotFound = errors.New("room not found")
var ErrUnauthorized = errors.New("invalid room token")

type Stroke struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	Color     string  `json:"color"`
	Width     float64 `json:"width"`
	Points    []Point `json:"points"`
	CreatedAt int64   `json:"createdAt"`
}
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type BoardStore interface {
	CreateRoom(context.Context, string, string) error
	Authorize(context.Context, string, string) error
	LoadStrokes(context.Context, string) ([]Stroke, error)
	SaveStroke(context.Context, string, Stroke) error
	Clear(context.Context, string) error
	Ping(context.Context) error
}

type RedisStore struct {
	client     *redis.Client
	ttl        time.Duration
	maxStrokes int64
}

func NewRedis(addr, password string, ttl time.Duration, maxStrokes int) *RedisStore {
	return &RedisStore{client: redis.NewClient(&redis.Options{Addr: addr, Password: password}), ttl: ttl, maxStrokes: int64(maxStrokes)}
}
func (s *RedisStore) Close() error                   { return s.client.Close() }
func (s *RedisStore) Ping(ctx context.Context) error { return s.client.Ping(ctx).Err() }
func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func NewToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *RedisStore) CreateRoom(ctx context.Context, roomID, token string) error {
	key := "room:" + roomID + ":meta"
	ok, err := s.client.SetNX(ctx, key, hashToken(token), s.ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("room already exists")
	}
	return nil
}
func (s *RedisStore) Authorize(ctx context.Context, roomID, token string) error {
	want, err := s.client.Get(ctx, "room:"+roomID+":meta").Result()
	if errors.Is(err, redis.Nil) {
		return ErrRoomNotFound
	}
	if err != nil {
		return err
	}
	got := hashToken(token)
	if len(want) != len(got) || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return ErrUnauthorized
	}
	_ = s.client.Expire(ctx, "room:"+roomID+":meta", s.ttl).Err()
	return nil
}
func (s *RedisStore) LoadStrokes(ctx context.Context, roomID string) ([]Stroke, error) {
	items, err := s.client.LRange(ctx, "room:"+roomID+":strokes", -s.maxStrokes, -1).Result()
	if err != nil {
		return nil, err
	}
	strokes := make([]Stroke, 0, len(items))
	for _, item := range items {
		var stroke Stroke
		if json.Unmarshal([]byte(item), &stroke) == nil {
			strokes = append(strokes, stroke)
		}
	}
	return strokes, nil
}
func (s *RedisStore) SaveStroke(ctx context.Context, roomID string, stroke Stroke) error {
	payload, err := json.Marshal(stroke)
	if err != nil {
		return err
	}
	key := "room:" + roomID + ":strokes"
	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, key, payload)
	pipe.LTrim(ctx, key, -s.maxStrokes, -1)
	pipe.Expire(ctx, key, s.ttl)
	pipe.Expire(ctx, "room:"+roomID+":meta", s.ttl)
	_, err = pipe.Exec(ctx)
	return err
}
func (s *RedisStore) Clear(ctx context.Context, roomID string) error {
	return s.client.Del(ctx, "room:"+roomID+":strokes").Err()
}
