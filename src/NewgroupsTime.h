#ifndef NEWSCACHE_NEWGROUPS_TIME_H
#define NEWSCACHE_NEWGROUPS_TIME_H

#include <time.h>

// Convert a validated NEWGROUPS date to an ActiveDB timestamp. Local dates
// use the DST rules for that date; pre-epoch thresholds are clamped to zero.
bool newgroups_time(struct tm value, bool gmt, time_t *result);

#endif
