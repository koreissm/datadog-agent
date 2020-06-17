// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package obfuscate implements quantizing and obfuscating of tags and resources for
// a set of spans matching a certain criteria.
package obfuscate

import (
	"bytes"
	"sync/atomic"

	"github.com/DataDog/datadog-agent/pkg/config"
	traceconfig "github.com/DataDog/datadog-agent/pkg/trace/config"
	"github.com/DataDog/datadog-agent/pkg/trace/pb"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

// Obfuscator quantizes and obfuscates spans. The obfuscator is not safe for
// concurrent use.
type Obfuscator struct {
	opts                 *traceconfig.ObfuscationConfig
	es                   *jsonObfuscator // nil if disabled
	mongo                *jsonObfuscator // nil if disabled
	sqlExecPlan          *jsonObfuscator // nil if disabled
	sqlExecPlanNormalize *jsonObfuscator // nil if disabled
	// sqlLiteralEscapes reports whether we should treat escape characters literally or as escape characters.
	// A non-zero value means 'yes'. Different SQL engines behave in different ways and the tokenizer needs
	// to be generic.
	// Not safe for concurrent use.
	sqlLiteralEscapes int32
}

// SetSQLLiteralEscapes sets whether or not escape characters should be treated literally by the SQL obfuscator.
func (o *Obfuscator) SetSQLLiteralEscapes(ok bool) {
	if ok {
		atomic.StoreInt32(&o.sqlLiteralEscapes, 1)
	} else {
		atomic.StoreInt32(&o.sqlLiteralEscapes, 0)
	}
}

// SQLLiteralEscapes reports whether escape characters should be treated literally by the SQL obfuscator.
func (o *Obfuscator) SQLLiteralEscapes() bool {
	return atomic.LoadInt32(&o.sqlLiteralEscapes) == 1
}

// NewObfuscator creates a new obfuscator
func NewObfuscator(cfg *traceconfig.ObfuscationConfig) *Obfuscator {
	if cfg == nil {
		cfg = new(traceconfig.ObfuscationConfig)
	}
	o := Obfuscator{opts: cfg}
	if cfg.ES.Enabled {
		o.es = o.newJSONObfuscator(&cfg.ES)
	}
	if cfg.Mongo.Enabled {
		o.mongo = o.newJSONObfuscator(&cfg.Mongo)
	}
	if cfg.SQLExecPlan.Enabled {
		o.sqlExecPlan = o.newJSONObfuscator(&cfg.SQLExecPlan)
	}
	if cfg.SQLExecPlanNormalize.Enabled {
		o.sqlExecPlanNormalize = o.newJSONObfuscator(&cfg.SQLExecPlanNormalize)
	}
	return &o
}

// LoadSQLObfuscator initializes the obfsucator from the standard agent config, setting defaults for SQLExecPlan
// and SQLExecPlanNormalize if they are not set. Since this relies on config.Datadog it must be called after
// config.Datadog has been initialized in order for config.Datadog to be used
func LoadSQLObfuscator() *Obfuscator {
	var cfg traceconfig.ObfuscationConfig
	if err := config.Datadog.UnmarshalKey("apm_config.obfuscation", &cfg); err != nil {
		log.Errorf("failed to unmarshal apm_config.obfuscation: %s", err.Error())
		cfg = traceconfig.ObfuscationConfig{}
	}
	if !cfg.SQLExecPlan.Enabled {
		cfg.SQLExecPlan = defaultSQLPlanObfuscateSettings
	}
	if !cfg.SQLExecPlanNormalize.Enabled {
		cfg.SQLExecPlanNormalize = defaultSQLPlanNormalizeSettings
	}
	return NewObfuscator(&cfg)
}

// Obfuscate may obfuscate span's properties based on its type and on the Obfuscator's
// configuration.
func (o *Obfuscator) Obfuscate(span *pb.Span) {
	switch span.Type {
	case "sql", "cassandra":
		o.obfuscateSQL(span)
	case "redis":
		o.quantizeRedis(span)
		if o.opts.Redis.Enabled {
			o.obfuscateRedis(span)
		}
	case "memcached":
		if o.opts.Memcached.Enabled {
			o.obfuscateMemcached(span)
		}
	case "web", "http":
		o.obfuscateHTTP(span)
	case "mongodb":
		o.obfuscateJSON(span, "mongodb.query", o.mongo)
	case "elasticsearch":
		o.obfuscateJSON(span, "elasticsearch.body", o.es)
	}
}

// compactWhitespaces compacts all whitespaces in t.
func compactWhitespaces(t string) string {
	n := len(t)
	r := make([]byte, n)
	spaceCode := uint8(32)
	isWhitespace := func(char uint8) bool { return char == spaceCode }
	nr := 0
	offset := 0
	for i := 0; i < n; i++ {
		if isWhitespace(t[i]) {
			copy(r[nr:], t[nr+offset:i])
			r[i-offset] = spaceCode
			nr = i + 1 - offset
			for j := i + 1; j < n; j++ {
				if !isWhitespace(t[j]) {
					offset += j - i - 1
					i = j
					break
				} else if j == n-1 {
					offset += j - i
					i = j
					break
				}
			}
		}
	}
	copy(r[nr:], t[nr+offset:n])
	r = r[:n-offset]
	return string(bytes.Trim(r, " "))
}

// defaultSQLPlanNormalizeSettings are the default JSON obfuscator settings for both obfuscating and normalizing SQL
// execution plans
var defaultSQLPlanNormalizeSettings = traceconfig.JSONObfuscationConfig{
	Enabled:         true,
	TransformerType: "obfuscate_sql",
	TransformValues: []string{
		// mysql
		"attached_condition",
		// postgres
		"Recheck Cond",
		"Merge Cond",
		"Hash Cond",
		"Join Filter",
	},
	KeepValues: []string{
		// mysql
		"select_id",
		"using_filesort",
		"table_name",
		"access_type",
		"possible_keys",
		"key",
		"key_length",
		"used_key_parts",
		"used_columns",
		"ref",
		"update",
		// postgres
		"Node Type",
		"Parallel Aware",
		"Scan Direction",
		"Index Name",
		"Relation Name",
		"Alias",
		"Parent Relationship",
		"Sort Key",
	},
}

// defaultSQLPlanObfuscateSettings builds upon sqlPlanNormalizeSettings by including cost & row estimates in the keep
// list
var defaultSQLPlanObfuscateSettings = traceconfig.JSONObfuscationConfig{
	Enabled:         true,
	TransformerType: "obfuscate_sql",
	KeepValues: append([]string{
		// mysql
		"cost_info",
		// postgres
		"Startup Cost",
		"Total Cost",
		"Plan Rows",
		"Plan Width",
	}, defaultSQLPlanNormalizeSettings.KeepValues...),
	TransformValues: defaultSQLPlanNormalizeSettings.TransformValues,
}
