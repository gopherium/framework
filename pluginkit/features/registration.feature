Feature: Registering compiled plugins one by one
  A downstream application can ask the wiring generator for a list of
  every backend plugin's id and Register function, in wiring order, in
  place of one function that registers them all. The application keeps
  the plugins it wants on, builds each one's dependencies from its id,
  and registers them one by one. A plugin whose Register fails or panics
  is reported by its id, and the plugins after it still register.

  @wip
  Scenario: The registration list names each backend plugin in wiring order
    Given the plugin "events" has a backend
    And the plugin "gallery" has a backend
    And the plugin "pretty" has only the frontend "@example/plugin-pretty"
    And the application asks for a registration list
    When the plugins are wired
    Then the registration list names "events" and "gallery", in that order
    And the Go wiring holds no registerPlugins function

  @wip
  Scenario: The registry holds an exported registration list in place of All
    Given the plugin "events" has a backend
    And the application asks for a registration list and a registry
    When the plugins are wired
    Then the registry's Registrations names "events"
    And the registry holds no All function

  @wip
  Scenario: Without a registration list the Go wiring keeps registerPlugins
    Given the plugin "events" has a backend
    When the plugins are wired
    Then the Go wiring holds the registerPlugins function
    And the Go wiring holds no registration list

  @wip
  Scenario: Only the plugins the application passes are registered
    Given the compiled plugins "events", "feed" and "gallery"
    And the application leaves "gallery" out
    When the application registers each plugin
    Then "events" and "feed" are registered, in that order
    And the Register of "gallery" never ran

  @wip
  Scenario: Each plugin receives the dependencies built for its own id
    Given the compiled plugins "events" and "feed"
    When the application registers each plugin
    Then the Register of "events" received the dependencies built for "events"
    And the Register of "feed" received the dependencies built for "feed"
    And dependencies were built for "events" and "feed", in that order, once each

  @wip
  Scenario: A plugin whose Register panics fails alone
    Given the compiled plugins "events", "feed" and "gallery"
    And the Register of "feed" panics with "boom"
    When the application registers each plugin
    Then "events" and "gallery" are registered, in that order
    And "feed" failed at the step "register" with: pluginkit: feed register panicked: boom
    And the failure of "feed" is marked as a panic

  @wip
  Scenario: A plugin whose Register answers an error fails alone
    Given the compiled plugins "events", "feed" and "gallery"
    And the Register of "feed" answers the error "the title is too long"
    When the application registers each plugin
    Then "events" and "gallery" are registered, in that order
    And "feed" failed at the step "register" with: pluginkit: feed register: the title is too long
    And the failure of "feed" is not marked as a panic

  @wip
  Scenario: An error that holds the text of a panic is not marked as a panic
    Given the compiled plugins "events" and "feed"
    And the Register of "feed" answers the error "pluginkit: feed register panicked: boom"
    When the application registers each plugin
    Then "feed" failed at the step "register" with: pluginkit: feed register: pluginkit: feed register panicked: boom
    And the failure of "feed" is not marked as a panic

  @wip
  Scenario: Registering no plugin answers an empty outcome
    Given no compiled plugin
    When the application registers each plugin
    Then no plugin is registered and none failed

  @wip
  Scenario: A panic while the application builds dependencies is not recovered
    Given the compiled plugins "events" and "feed"
    And building the dependencies of "feed" panics with "no settings for feed"
    When the application registers each plugin
    Then the registering panics with "no settings for feed"
    And the Register of "feed" never ran

  @wip
  Scenario Outline: The generated list builds and registers each plugin
    Given an SDK whose Plugin is <sdk plugin>
    And the plugin "events" has a backend whose Register answers a plugin
    And the plugin "feed" has a backend whose Register answers the error "no database"
    And the application asks for a registration list
    When the plugins are wired, built and registered one by one
    Then "events" is registered
    And "feed" failed at the step "register" with: pluginkit: feed register: no database

    Examples:
      | sdk plugin                       |
      | an alias of the pluginkit Plugin |
      | an interface of its own          |
