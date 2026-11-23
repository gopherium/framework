Feature: Listing the installed plugins
  An application lists the plugins it holds on a Plugins screen, as
  WordPress lists its installed plugins. The list joins the manifest of
  each compiled plugin with the record the store keeps of it. Each row
  holds the plugin's id, the name, description and version its manifest
  gives, whether it declares any settings, and the state its record
  holds. A paused row also holds the step the plugin failed at and its
  own error, kept apart, so a screen can build its sentence for the step
  from a translated template. A plugin the application has not installed
  and a record whose plugin is no longer compiled are left out.

  Scenario: An active plugin is listed with the name, description and version of its manifest
    Given the compiled plugins "feed"
    And the manifest of "feed" names it "Feed"
    And the manifest of "feed" describes it as "Serves the published posts as an RSS feed."
    And the manifest of "feed" gives the version "0.0.1"
    And the store records "feed" as active
    When the application lists the installed plugins
    Then the list holds "feed" as "active"

  Scenario: A paused plugin is listed with the step it failed at and its own error
    Given the compiled plugins "feed"
    And the manifest of "feed" names it "Feed"
    And the store records "feed" as paused at "register" with "the title is too long"
    When the application lists the installed plugins
    Then the list holds "feed" as paused at "register", its reason reading "the title is too long"

  Scenario: An inactive plugin is listed as inactive
    Given the compiled plugins "feed"
    And the manifest of "feed" names it "Feed"
    And the store records "feed" as inactive
    When the application lists the installed plugins
    Then the list holds "feed" as "inactive"

  Scenario: A plugin the store records as not installed is left out
    Given the compiled plugins "events" and "feed"
    And the manifest of "events" names it "Events"
    And the manifest of "feed" names it "Feed"
    And the store records "events" as active
    And the store records "feed" as not installed
    When the application lists the installed plugins
    Then the list holds "events" as "active"
    And the list leaves out "feed"

  Scenario: A record of a plugin that is no longer compiled is left out
    Given the compiled plugins "events"
    And the manifest of "events" names it "Events"
    And the store records "events" and "gallery" as active
    When the application lists the installed plugins
    Then the list holds "events" as "active"
    And the list leaves out "gallery"

  Scenario: A compiled plugin with no record is listed as active
    Given the compiled plugins "feed"
    And the manifest of "feed" names it "Feed"
    And the store holds no record
    When the application lists the installed plugins
    Then the list holds "feed" as "active"

  Scenario: A manifest with no description or version lists both as empty
    Given the compiled plugins "feed"
    And the manifest of "feed" names it "Feed"
    And the store records "feed" as active
    When the application lists the installed plugins
    Then the list holds "feed" as "active" with no description and no version

  Scenario: A plugin whose manifest declares a setting is listed with settings
    Given the compiled plugins "events" and "feed"
    And the manifest of "events" names it "Events"
    And the manifest of "feed" names it "Feed"
    And the manifest of "feed" declares the setting "title"
    And the store holds no record
    When the application lists the installed plugins
    Then the list holds "feed" with settings
    And the list holds "events" without settings

  Scenario: The plugins are listed by name, then by id
    Given the compiled plugins "events", "feed" and "gallery"
    And the manifest of "events" names it "Calendar"
    And the manifest of "feed" names it "Atom"
    And the manifest of "gallery" names it "Calendar"
    And the store holds no record
    When the application lists the installed plugins
    Then the list names "feed", "events" and "gallery", in that order
