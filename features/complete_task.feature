Feature: Completing a Task
  Only the Station holding the active claim may complete the Task it pulled;
  anyone else is rejected, and a completed Task leaves the queue for good.

  Background:
    Given a running Fulfillment Execution service
    And a Station "pick-01" is registered with capabilities "pick"
    And a Station "pick-02" is registered with capabilities "pick"
    And a "PICK" Task for order "order-1" with a CPT 30 minutes from now requiring capabilities "pick"
    And Station "pick-01" has claimed the next "PICK" Task

  @bdd
  Scenario: Completing a claimed task succeeds for the owning station
    When Station "pick-01" completes the claimed Task
    Then the response status is 204
    And a "TaskCompleted" domain event is recorded
    And the queue depth for "PICK" is 0

  @bdd
  Scenario: Completing a task without owning the claim is rejected
    When Station "pick-02" completes the claimed Task
    Then the response status is 409
    And the response is a Problem Details document of type "task-not-owner"
    And no "TaskCompleted" domain event is recorded
