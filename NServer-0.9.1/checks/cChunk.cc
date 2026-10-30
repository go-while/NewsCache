// Checks the chunk arithmetic of the overview cache (Newsgroup.h) at the
// top of the 64-bit range. "a / CHUNK * CHUNK + CHUNK" wrapped around to
// a small number there, so a loop over the chunks of [f, ARTNUM_MAX]
// started over from 8384 and ran for about 1.8*10^15 chunks. The fake
// news server of the e2e tests can't send such numbers (int64).

#include <iostream>

#include "Logger.h"
#include "Newsgroup.h"

using namespace std;

Logger slog;

static int failures = 0;

#define CHECK(cond) do { \
	if (!(cond)) { \
		cerr << __FILE__ << ":" << __LINE__ << ": failed: " #cond "\n"; \
		failures++; \
	} \
} while (0)

// The number of chunks the loop of CNewsgroup::print / RNewsgroup visits
// for [f,l], or -1 if it doesn't end within max steps.
static long long chunks(artnum_t f, artnum_t l, long long max)
{
	long long n = 0;
	for (artnum_t a = f;;) {
		artnum_t b = chunk_last(a);
		if (b > l)
			b = l;
		if (b < a || ++n > max)
			return -1;
		if (b >= l)
			break;
		a = next_chunk(a);
		if (a == 0)
			break;
	}
	return n;
}

int main()
{
	CHECK(next_chunk(0) == OVERVIEW_CHUNK);
	CHECK(next_chunk(OVERVIEW_CHUNK - 1) == OVERVIEW_CHUNK);
	CHECK(next_chunk(OVERVIEW_CHUNK) == 2 * OVERVIEW_CHUNK);
	CHECK(chunk_last(0) == OVERVIEW_CHUNK - 1);
	CHECK(chunk_last(67486767815ULL) == 67486769999ULL);

	// the last, shorter chunk ends at ARTNUM_MAX; there is none after it
	CHECK(chunk_last(ARTNUM_MAX) == ARTNUM_MAX);
	CHECK(next_chunk(ARTNUM_MAX) == 0);
	artnum_t top = ARTNUM_MAX / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
	CHECK(chunk_last(top) == ARTNUM_MAX);
	CHECK(next_chunk(top) == 0);
	CHECK(next_chunk(top - 1) == top);
	CHECK(chunk_last(top - 1) == top - 1);
	CHECK(chunk_last(ARTNUM_MAX - 12345) >= ARTNUM_MAX - 12345);

	// loops over chunks end, with the right count
	CHECK(chunks(1, 25000, 100) == 3);
	CHECK(chunks(ARTNUM_MAX - 25000, ARTNUM_MAX, 100) == 4);
	CHECK(chunks(top, ARTNUM_MAX, 100) == 1);
	CHECK(chunks(ARTNUM_MAX, ARTNUM_MAX, 100) == 1);

	// a group's range from a news server is cut below the top
	CHECK(ARTNUM_LIMIT < top);
	CHECK(next_chunk(ARTNUM_LIMIT) != 0);
	CHECK(ARTNUM_MAX - ARTNUM_LIMIT == 2 * OVERVIEW_CHUNK);

	if (failures) {
		cerr << failures << " checks failed\n";
		return 1;
	}
	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
