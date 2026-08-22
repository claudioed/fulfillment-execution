Feature: Lease lifecycle
  A claim on a Task is time-boxed by a Lease. The owning Station may renew
  the Lease to keep the work; if the Lease expires unconfirmed, the sweep
  returns the Task to the pool so work is never silently lost.

  Background:
    Given a running Fulfillment Execution service
    And a Station "pick-01" is registered with capabilities "pick"
    And a "PICK" Task for order "order-1" with a CPT 30 minutes from now requiring capabilities "pick"
    And Station "pick-01" has claimed the next "PICK" Task

  @bdd
  Scenario: Renewing a lease before expiry extends the claim
    When the clock advances by 3 minutes
    And Station "pick-01" renews the lease on the claimed Task
    Then the response status is 204
    When the clock advances by 3 minutes
    And the lease expiry sweep runs
    Then the response status is 200
    And 0 leases were freed
    And the queue depth for "PICK" is 0
    And no "LeaseExpired" domain event is recorded

  @bdd
  Scenario: An expired lease returns the task to the pool
    When the clock advances by 6 minutes
    And the lease expiry sweep runs
    Then the response status is 200
    And 1 lease was freed
    And a "LeaseExpired" domain event is recorded
    And the queue depth for "PICK" is 1
    When Station "pick-01" calls claimNext for task type "PICK"
    Then the response status is 200
    And the claimed Task is the one for order "order-1"
