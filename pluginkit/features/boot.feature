Feature: Booting plugins and pausing the one that fails
  An application keeps the state of each compiled plugin between boots:
  not installed, inactive, active or paused. Inactive means turned off on
  purpose. Paused means the boot stopped the plugin after it failed, as
  WordPress's recovery mode does. Each boot reads the records once,
  registers the active and paused plugins, lets the application run its
  own steps, then starts them. A plugin that fails is paused with its
  error, and the others still start. A paused plugin is tried again at
  every boot.

  Scenario: The first boot records each compiled plugin as active
    Given the compiled plugins "events" and "feed"
    And the store holds no record
    When the application boots its plugins
    Then "events" and "feed" started, in that order
    And the store records "events" as active with no error
    And the store records "feed" as active with no error

  Scenario: A boot where nothing changes writes nothing to the store
    Given the compiled plugins "events" and "feed"
    And the store records "events" and "feed" as active
    When the application boots its plugins
    Then "events" and "feed" started, in that order
    And the store was not written

  Scenario: A plugin whose Register fails is paused and the others start
    Given the compiled plugins "events", "feed" and "gallery"
    And the Register of "feed" fails with "the title is too long"
    When the application boots its plugins
    Then "events" and "gallery" started, in that order
    And the store records "feed" as paused with "pluginkit: feed register: the title is too long"

  Scenario: A plugin whose Register panics is paused
    Given the compiled plugins "events" and "feed"
    And the Register of "feed" panics with "boom"
    When the application boots its plugins
    Then "events" started
    And the store records "feed" as paused with "pluginkit: feed register panicked: boom"

  Scenario: A plugin that calls itself by another id is paused under its entry's id
    Given the compiled plugins "events" and "feed"
    And the Register of "feed" answers a plugin that calls itself "news"
    When the application boots its plugins
    Then "events" started
    And the store records "feed" as paused with "pluginkit: feed register: the plugin calls itself "news""
    And the store holds no record of "news"

  Scenario: A plugin whose migration fails is paused and never starts
    Given the compiled plugins "events" and "feed"
    And the migration of "feed" fails with "schema exploded"
    When the application boots its plugins
    Then "events" started
    And "feed" never started
    And the store records "feed" as paused with "pluginkit: feed migrate: schema exploded"

  Scenario: A plugin whose Start fails is paused
    Given the compiled plugins "events" and "feed"
    And the Start of "feed" fails with "no route table"
    When the application boots its plugins
    Then "events" started
    And the store records "feed" as paused with "pluginkit: feed start: no route table"

  Scenario: A plugin whose migration record fails still starts and stays active
    Given the compiled plugins "events" and "feed"
    And the store records "events" and "feed" as active
    And the migration record of "feed" fails with "ledger locked"
    When the application boots its plugins
    Then "events" and "feed" started, in that order
    And the store records "feed" as active with no error
    And the store was not written

  Scenario Outline: A plugin the store keeps <state> is never registered
    Given the compiled plugins "events" and "feed"
    And the store records "events" as active
    And the store records "feed" as <state>
    When the application boots its plugins
    Then "events" started
    And the Register of "feed" never ran
    And the store records "feed" as <state> with no error

    Examples:
      | state         |
      | inactive      |
      | not installed |

  Scenario: A paused plugin that now starts is active again with no error
    Given the compiled plugins "events" and "feed"
    And the store records "events" as active
    And the store records "feed" as paused with "a setting is missing"
    When the application boots its plugins
    Then "events" and "feed" started, in that order
    And the store records "feed" as active with no error

  Scenario: A paused plugin that fails again stays paused with its new error
    Given the compiled plugins "events" and "feed"
    And the store records "events" as active
    And the store records "feed" as paused with "a setting is missing"
    And the Start of "feed" fails with "no route table"
    When the application boots its plugins
    Then "events" started
    And the store records "feed" as paused with "pluginkit: feed start: no route table"

  Scenario: The application pauses a plugin for a reason of its own
    Given the compiled plugins "events" and "forum"
    And the store records "events" and "forum" as active
    And the application finds that "forum" declares a role it cannot accept
    When the application boots its plugins
    Then "events" started
    And "forum" never started
    And the store records "forum" as paused with "forum declares a role it cannot accept"

  Scenario: A boot whose context ends pauses nothing
    Given the compiled plugins "events", "feed" and "gallery"
    And the store records "events", "feed" and "gallery" as active
    And the context of the boot ends while "feed" starts
    When the application boots its plugins
    Then the boot reports that its context ended
    And the store was not written

  Scenario: A boot whose context ends while a plugin registers pauses nothing
    Given the compiled plugins "events" and "feed"
    And the store records "events" and "feed" as active
    And the context of the boot ends while "feed" registers
    And the Register of "feed" fails with "the boot is shutting down"
    When the application boots its plugins
    Then the boot reports that its context ended
    And the store was not written

  Scenario: A store that cannot read its records stops the boot
    Given the compiled plugins "events" and "feed"
    And the store fails to read with "connection dropped"
    When the application boots its plugins
    Then the boot fails with: pluginkit: read the plugin records: connection dropped
    And the Register of "events" never ran

  Scenario: A store that cannot write a pause stops the boot
    Given the compiled plugins "events" and "feed"
    And the store records "events" and "feed" as active
    And the Register of "feed" fails with "the title is too long"
    And the store fails to write with "connection dropped"
    When the application boots its plugins
    Then the boot fails with: pluginkit: pause feed: connection dropped
