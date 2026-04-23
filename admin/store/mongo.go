package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoDeviceStore implements DeviceStore using MongoDB v2 driver.
//
// Schema (one doc per device):
//
//	_id            string DeviceID                ("dev-XXXXXXXXXXXX")
//	fingerprint    string (android_id|imei|serial, may be "")
//	os, brand, model, imei, android_id, serial_number
//	first_register_at, last_register_at  ISODate
//	last_client_ts int64 (ms)
//	last_remote    string
//	register_count int64
//
// Indexes:
//   - unique on _id (implicit)
//   - unique on fingerprint where fingerprint != ""
type MongoDeviceStore struct {
	client *mongo.Client
	col    *mongo.Collection
}

// NewMongoDeviceStore connects, pings, ensures indexes, and returns a store.
// Caller is responsible for Close().
func NewMongoDeviceStore(ctx context.Context, uri, db, collection string) (*MongoDeviceStore, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	col := client.Database(db).Collection(collection)
	if err := ensureIndexes(ctx, col); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return &MongoDeviceStore{client: client, col: col}, nil
}

func ensureIndexes(ctx context.Context, col *mongo.Collection) error {
	idxCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Unique on fingerprint when present.
	if _, err := col.Indexes().CreateOne(idxCtx, mongo.IndexModel{
		Keys: bson.D{{Key: "fingerprint", Value: 1}},
		Options: options.Index().
			SetName("uniq_fingerprint").
			SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "fingerprint", Value: bson.D{{Key: "$gt", Value: ""}}}}),
	}); err != nil {
		return fmt.Errorf("mongo index uniq_fingerprint: %w", err)
	}
	// Scheduling-friendly index on geo + freshness (the future scheduler will
	// query "give me an active US device sorted by last_seen desc").
	if _, err := col.Indexes().CreateOne(idxCtx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "network.country", Value: 1},
			{Key: "network.last_seen_at", Value: -1},
		},
		Options: options.Index().SetName("country_lastseen"),
	}); err != nil {
		return fmt.Errorf("mongo index country_lastseen: %w", err)
	}
	return nil
}

// Close disconnects from Mongo.
func (s *MongoDeviceStore) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Disconnect(ctx)
}

// UpsertDevice implements DeviceStore.
func (s *MongoDeviceStore) UpsertDevice(ctx context.Context, info DeviceInfo) (*Device, error) {
	now := time.Now().UTC()
	fp := Fingerprint(info)

	// Without a fingerprint, dedup is impossible -> always insert a new doc.
	if fp == "" {
		id, err := NewDeviceID()
		if err != nil {
			return nil, err
		}
		doc := bson.M{
			"_id":               id,
			"fingerprint":       "",
			"os":                info.OS,
			"brand":             info.Brand,
			"model":             info.Model,
			"imei":              info.IMEI,
			"android_id":        info.AndroidID,
			"serial_number":     info.SerialNumber,
			"first_register_at": now,
			"last_register_at":  now,
			"last_client_ts":    info.ClientTimestampMs,
			"last_remote":       info.RemoteAddr,
			"register_count":    int64(1),
		}
		if _, err := s.col.InsertOne(ctx, doc); err != nil {
			return nil, fmt.Errorf("mongo insert (no fp): %w", err)
		}
		return &Device{
			DeviceID:            id,
			OS:                  info.OS,
			Brand:               info.Brand,
			Model:               info.Model,
			IMEI:                info.IMEI,
			AndroidID:           info.AndroidID,
			SerialNumber:        info.SerialNumber,
			FirstRegisterAt:     now,
			LastRegisterAt:      now,
			LastClientTimestamp: info.ClientTimestampMs,
			LastRemoteAddr:      info.RemoteAddr,
			RegisterCount:       1,
		}, nil
	}

	// With a fingerprint, do find_one_and_update upsert. We need a fresh
	// DeviceID for the *insert* path (setOnInsert), and bump counters on
	// every call (set/inc).
	newID, err := NewDeviceID()
	if err != nil {
		return nil, err
	}
	update := bson.M{
		"$setOnInsert": bson.M{
			"_id":               newID,
			"fingerprint":       fp,
			"first_register_at": now,
		},
		"$set": bson.M{
			"os":               info.OS,
			"brand":            info.Brand,
			"model":            info.Model,
			"imei":             info.IMEI,
			"android_id":       info.AndroidID,
			"serial_number":    info.SerialNumber,
			"last_register_at": now,
			"last_client_ts":   info.ClientTimestampMs,
			"last_remote":      info.RemoteAddr,
		},
		"$inc": bson.M{"register_count": int64(1)},
	}
	opt := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)
	res := s.col.FindOneAndUpdate(ctx, bson.M{"fingerprint": fp}, update, opt)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo upsert returned no doc")
		}
		return nil, fmt.Errorf("mongo upsert: %w", err)
	}
	var raw bson.M
	if err := res.Decode(&raw); err != nil {
		return nil, fmt.Errorf("mongo decode: %w", err)
	}
	return decodeDevice(raw), nil
}

// UpdateDeviceNetwork merges a NetworkSnapshot into devices.<deviceID>.network.
// Only non-empty / non-nil fields are written, so a heartbeat without
// avg_rtt/loss_rate (insufficient samples) doesn't clobber a previously
// reported value.
func (s *MongoDeviceStore) UpdateDeviceNetwork(ctx context.Context, deviceID string, snap NetworkSnapshot) error {
	if deviceID == "" {
		return fmt.Errorf("store: empty device_id")
	}
	set := bson.M{}
	if snap.LastIP != "" {
		set["network.last_ip"] = snap.LastIP
	}
	if snap.NetType != "" {
		set["network.net_type"] = snap.NetType
	}
	if snap.AvgRTTms != nil {
		set["network.avg_rtt_ms"] = *snap.AvgRTTms
	}
	if snap.LossRate != nil {
		set["network.loss_rate"] = *snap.LossRate
	}
	if snap.Country != "" {
		set["network.country"] = snap.Country
	}
	if snap.CountryName != "" {
		set["network.country_name"] = snap.CountryName
	}
	if snap.Region != "" {
		set["network.region"] = snap.Region
	}
	if snap.RegionCode != "" {
		set["network.region_code"] = snap.RegionCode
	}
	if snap.City != "" {
		set["network.city"] = snap.City
	}
	if snap.CurTsMs > 0 {
		set["network.cur_ts_ms"] = snap.CurTsMs
	}
	// last_seen_at always advances on every heartbeat.
	if snap.ServerRecvMs > 0 {
		set["network.last_seen_at"] = time.UnixMilli(snap.ServerRecvMs).UTC()
	} else {
		set["network.last_seen_at"] = time.Now().UTC()
	}

	res, err := s.col.UpdateOne(
		ctx,
		bson.M{"_id": deviceID},
		bson.M{"$set": set},
	)
	if err != nil {
		return fmt.Errorf("mongo update network: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrUnknownDevice
	}
	return nil
}

func decodeDevice(m bson.M) *Device {
	d := &Device{}
	if v, ok := m["_id"].(string); ok {
		d.DeviceID = v
	}
	if v, ok := m["fingerprint"].(string); ok {
		d.Fingerprint = v
	}
	if v, ok := m["os"].(string); ok {
		d.OS = v
	}
	if v, ok := m["brand"].(string); ok {
		d.Brand = v
	}
	if v, ok := m["model"].(string); ok {
		d.Model = v
	}
	if v, ok := m["imei"].(string); ok {
		d.IMEI = v
	}
	if v, ok := m["android_id"].(string); ok {
		d.AndroidID = v
	}
	if v, ok := m["serial_number"].(string); ok {
		d.SerialNumber = v
	}
	if v, ok := m["first_register_at"].(bson.DateTime); ok {
		d.FirstRegisterAt = v.Time().UTC()
	}
	if v, ok := m["last_register_at"].(bson.DateTime); ok {
		d.LastRegisterAt = v.Time().UTC()
	}
	if v, ok := m["last_client_ts"].(int64); ok {
		d.LastClientTimestamp = v
	}
	if v, ok := m["last_remote"].(string); ok {
		d.LastRemoteAddr = v
	}
	if v, ok := m["register_count"].(int64); ok {
		d.RegisterCount = v
	} else if v, ok := m["register_count"].(int32); ok {
		d.RegisterCount = int64(v)
	}
	return d
}
