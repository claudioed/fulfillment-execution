Feature: claimNext pull dispatch
  Fulfillment Execution is pull, not push: a Station asks for the next Task
  and the system selects the work — never the other way round. The Task
  handed back is the earliest-CPT pending Task whose required capabilities
  the Station holds, and it is leased so assignment is at-most-once.

  Background:
    Given a running Fulfillment Execution service

  @bdd
  Scenario: Claiming next task returns the earliest-CPT pending task the station is capable of
    Given a Station "pick-01" is registered with capabilities "pick"
    And a "PICK" Task for order "order-late" with a CPT 120 minutes from now requiring capabilities "pick"
    And a "PICK" Task for order "order-early" with a CPT 30 minutes from now requiring capabilities "pick"
    When Station "pick-01" calls claimNext for task type "PICK"
    Then the response status is 200
    And the claimed Task is the one for order "order-early"
    And the claimed Task is leased to Station "pick-01"
    And a "TaskClaimed" domain event is recorded
    And the queue depth for "PICK" is 1

  @bdd
  Scenario: Claiming next task with mismatched capabilities is rejected/skipped
    Given a Station "pack-01" is registered with capabilities "pack"
    And a "PICK" Task for order "order-1" with a CPT 30 minutes from now requiring capabilities "pick"
    When Station "pack-01" calls claimNext for task type "PICK"
    Then the response status is 409
    And the response is a Problem Details document of type "no-claimable-task"
    And no "TaskClaimed" domain event is recorded
    And the queue depth for "PICK" is 1

  @bdd
  Scenario: A task cannot be claimed twice while its lease is active
    Given a Station "pick-01" is registered with capabilities "pick"
    And a Station "pick-02" is registered with capabilities "pick"
    And a "PICK" Task for order "order-1" with a CPT 30 minutes from now requiring capabilities "pick"
    And Station "pick-01" has claimed the next "PICK" Task
    When Station "pick-02" calls claimNext for task type "PICK"
    Then the response status is 409
    And the response is a Problem Details document of type "no-claimable-task"
    And the claimed Task is leased to Station "pick-01"
