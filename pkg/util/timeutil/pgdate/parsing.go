// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License included
// in the file licenses/BSL.txt and at www.mariadb.com/bsl11.
//
// Change Date: 2022-10-01
//
// On the date above, in accordance with the Business Source License, use
// of this software will be governed by the Apache License, Version 2.0,
// included in the file licenses/APL.txt and at
// https://www.apache.org/licenses/LICENSE-2.0

package pgdate

import (
	"math"
	"time"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

// Various keywords that appear in timestamps.
const (
	// Alias for UTC.  No idea who actually uses this.
	keywordAllBalls = "allballs"
	keywordAM       = "am"
	keywordEpoch    = "epoch"
	keywordEraAD    = "ad"
	keywordEraBC    = "bc"
	keywordEraBCE   = "bce"
	keywordEraCE    = "ce"
	// Alias for UTC.
	keywordGMT       = "gmt"
	keywordInfinity  = "infinity"
	keywordNow       = "now"
	keywordPM        = "pm"
	keywordToday     = "today"
	keywordTomorrow  = "tomorrow"
	keywordUTC       = "utc"
	keywordYesterday = "yesterday"
	// Alias for UTC.
	keywordZ = "z"
	// Alias for UTC.
	keywordZulu = "zulu"
)

// Commonly-used collections of fields.
var (
	dateFields         = newFieldSet(fieldYear, fieldMonth, fieldDay, fieldEra)
	dateRequiredFields = newFieldSet(fieldYear, fieldMonth, fieldDay)

	timeFields = newFieldSet(
		fieldHour, fieldMinute, fieldSecond, fieldNanos, fieldMeridian,
		fieldTZHour, fieldTZMinute, fieldTZSecond)
	timeRequiredFields = newFieldSet(fieldHour, fieldMinute)

	dateTimeFields = dateFields.AddAll(timeFields)

	tzFields = newFieldSet(fieldTZHour, fieldTZMinute, fieldTZSecond)
)

// These are sentinel values for handling special values:
// https://www.postgresql.org/docs/10/static/datatype-datetime.html#DATATYPE-DATETIME-SPECIAL-TABLE
var (
	TimeEpoch            = timeutil.Unix(0, 0)
	TimeInfinity         = timeutil.Unix(math.MaxInt64, math.MaxInt64)
	TimeNegativeInfinity = timeutil.Unix(math.MinInt64, math.MinInt64)
)

//go:generate stringer -type=ParseMode

// ParseMode controls the resolution of ambiguous date formats such as
// `01/02/03`.
type ParseMode uint

// These are the various parsing modes that determine in which order
// we should look for years, months, and date.
// ParseModeYMD is the default value.
const (
	ParseModeYMD ParseMode = iota
	ParseModeDMY
	ParseModeMDY
)

// ParseDate converts a string into Date.
func ParseDate(now time.Time, mode ParseMode, s string) (Date, error) {
	fe := fieldExtract{
		now:      now,
		mode:     mode,
		required: dateRequiredFields,
		// We allow time fields to be provided since they occur after
		// the date fields that we're really looking for and for
		// time values like 24:00:00, would push into the next day.
		wanted: dateTimeFields,
	}

	if err := fe.Extract(s); err != nil {
		return Date{}, parseError(err, "date", s)
	}
	return fe.MakeDate()
}

// ParseTime converts a string into a time value on the epoch day.
func ParseTime(now time.Time, mode ParseMode, s string) (time.Time, error) {
	fe := fieldExtract{
		now:      now,
		required: timeRequiredFields,
		wanted:   timeFields,
	}

	if err := fe.Extract(s); err != nil {
		// It's possible that the user has given us a complete
		// timestamp string; let's try again, accepting more fields.
		fe = fieldExtract{
			now:      now,
			mode:     mode,
			required: timeRequiredFields,
			wanted:   dateTimeFields,
		}

		if err := fe.Extract(s); err != nil {
			return TimeEpoch, parseError(err, "time", s)
		}
	}
	return fe.MakeTime(), nil
}

// ParseTimestamp converts a string into a timestamp.
func ParseTimestamp(now time.Time, mode ParseMode, s string) (time.Time, error) {
	fe := fieldExtract{
		mode: mode,
		now:  now,
		// A timestamp only actually needs a date component; the time
		// would be midnight.
		required: dateRequiredFields,
		wanted:   dateTimeFields,
	}

	if err := fe.Extract(s); err != nil {
		return TimeEpoch, parseError(err, "timestamp", s)
	}
	return fe.MakeTimestamp(), nil
}

// badFieldPrefixError constructs a CodeInvalidDatetimeFormatError pgerror.
func badFieldPrefixError(field field, prefix rune) error {
	return inputErrorf("unexpected separator '%s' for field %s", string(prefix), field.Pretty())
}

// inputErrorf returns a CodeInvalidDatetimeFormatError pgerror.
func inputErrorf(format string, args ...interface{}) error {
	return pgerror.Newf(pgerror.CodeInvalidDatetimeFormatError, format, args...)
}

// outOfRangeError returns a CodeDatetimeFieldOverflowError pgerror.
func outOfRangeError(field string, val int) error {
	return pgerror.Newf(pgerror.CodeDatetimeFieldOverflowError,
		"field %s value %d is out of range", field, val)
}

// parseError ensures that any error we return to the client will
// be some kind of pgerror.
func parseError(err error, kind string, s string) error {
	if err, ok := err.(*pgerror.Error); ok {
		err.Message += " as type " + kind
		return err
	}
	return pgerror.Newf(pgerror.CodeInvalidDatetimeFormatError,
		`could not parse "%s" as type %s`, s, kind)
}
