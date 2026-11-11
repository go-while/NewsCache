Other changes:
* TestActiveRefreshBlocksGroup starts the waiting session's GROUP once
  the news server has the refresh's LIST ACTIVE, instead of 300 ms after
  the request. When the refresh began later than that, the GROUP ran
  before it and passed without testing anything: with the lock held
  across the fetch and the fetch half a second late, the old test
  passed and this one fails.
