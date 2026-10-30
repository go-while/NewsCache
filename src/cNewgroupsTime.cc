#include "config.h"
#include "NewgroupsTime.h"

#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <iostream>

struct TimeCase {
	const char *name;
	int year, month, day, hour, minute, second;
	bool gmt;
	time_t expected;
};

int main()
{
	if (setenv("TZ", "EST5EDT,M3.2.0,M11.1.0", 1) != 0) {
		std::cerr << "cannot set TZ\n";
		return 1;
	}
	tzset();
	const TimeCase cases[] = {
		{"GMT winter", 2026, 1, 15, 12, 0, 0, true, 1768478400},
		{"GMT summer", 2026, 7, 15, 12, 0, 0, true, 1784116800},
		{"EST winter", 2026, 1, 15, 12, 0, 0, false, 1768496400},
		{"EDT summer", 2026, 7, 15, 12, 0, 0, false, 1784131200},
		{"before spring transition", 2026, 3, 8, 1, 59, 59, false, 1772953199},
		{"after spring transition", 2026, 3, 8, 3, 0, 0, false, 1772953200},
		{"before autumn transition", 2026, 11, 1, 0, 59, 59, false, 1793509199},
		{"after autumn transition", 2026, 11, 1, 2, 0, 0, false, 1793516400},
		{"valid timestamp minus one", 1969, 12, 31, 23, 59, 59, true, 0},
	};
	int failures = 0;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		struct tm value;
		memset(&value, 0, sizeof(value));
		value.tm_year = cases[i].year - 1900;
		value.tm_mon = cases[i].month - 1;
		value.tm_mday = cases[i].day;
		value.tm_hour = cases[i].hour;
		value.tm_min = cases[i].minute;
		value.tm_sec = cases[i].second;
		time_t result = 0;
		if (!newgroups_time(value, cases[i].gmt, &result) ||
		    result != cases[i].expected) {
			std::cerr << cases[i].name << ": got " << result
			    << ", want " << cases[i].expected << "\n";
			++failures;
		}
	}
	return failures ? 1 : 0;
}
