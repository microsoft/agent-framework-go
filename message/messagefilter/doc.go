// Copyright (c) Microsoft. All rights reserved.

// Package messagefilter provides composable filters over slices of messages,
// used to select which messages a context provider stores or forwards. Filters
// compose with And and Or and remove messages from the input in place.
package messagefilter
