Feature: CPT-missed sweep
  A task still open (Pending or Claimed) past its CPT is detected by a
  Clock-driven sweep and reported via TaskCPTMissed — fulfillment-execution's
  half of order-management's promise feedback loop (ADR-0025).

  Background:
    Given a running Fulfillment Execution service
    And a "PICK" Task for order "order-1" with a CPT 5 minutes from now requiring capabilities "pick"

  @bdd
  Scenario: A task past its CPT is reported as missed
    When the clock advances by 6 minutes
    And the CPT-missed sweep runs
    Then the response status is 200
    And 1 task was reported as CPT-missed
    And a "TaskCPTMissed" domain event is recorded

  @bdd
  Scenario: A task not yet due is not reported
    When the CPT-missed sweep runs
    Then the response status is 200
    And 0 tasks were reported as CPT-missed
    And no "TaskCPTMissed" domain event is recorded

  @bdd
  Scenario: A still-overdue task is reported again on the next sweep pass
    When the clock advances by 6 minutes
    And the CPT-missed sweep runs
    Then 1 task was reported as CPT-missed
    When the CPT-missed sweep runs
    Then 1 task was reported as CPT-missed
