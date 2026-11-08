Feature: Serving each plugin's routes and reading them at boot
  An application mounts each plugin's routes behind its own middleware, and
  the paths the plugin declares public skip that middleware. A panic in the
  plugin's handler is caught on both. While nothing of the answer went out,
  the client gets the application's own answer instead. Once the answer
  had begun, the connection is cut, so a half answer never looks complete.
  The report is the application's own function that receives each panic
  with the plugin's id and its stack. At boot, each plugin's routes are
  read on their own, so the application can pause a plugin whose routes
  fail to load before it starts.

  Scenario: A route that answers normally is served untouched
    Given the plugin "feed" serves "/items" with a handler that answers status 201 and "created"
    When a client asks "feed" for "/items"
    Then the client gets status 201 and "created"
    And the middleware ran
    And the report holds nothing

  Scenario: A route that panics before it answers gets the application's answer
    Given the plugin "feed" serves "/items" with a handler that panics with "boom"
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And the report holds one failure of "feed" for GET "/items" that the application answered
    And that failure reads "pluginkit: feed route panicked: boom" with the stack of the panic

  Scenario: Headers the route set before it panicked stay off the application's answer
    Given the plugin "feed" serves "/items" with a handler that sets the header "X-Feed" and a cookie, then panics with "boom"
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And the answer holds no header "X-Feed" and no cookie
    And the answer holds the header the middleware set

  Scenario: An informational answer does not begin the answer
    Given the plugin "feed" serves "/items" with a handler that sends 103 Early Hints, then panics with "boom"
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And the report holds one failure of "feed" for GET "/items" that the application answered

  Scenario Outline: A route that panics after its answer began is cut
    Given the plugin "feed" serves "/items" with a handler that <begins>, then panics with "boom"
    When a client asks "feed" for "/items"
    Then the client's answer is cut
    And the report holds one failure of "feed" for GET "/items" that the application did not answer

    Examples:
      | begins                                        |
      | writes the status 200                         |
      | writes the body "partial"                     |
      | flushes the body "partial"                    |
      | hijacks the connection                        |
      | hijacks the connection through http.Hijacker  |

  Scenario Outline: A route that aborts on purpose is left to the server
    Given the plugin "feed" serves "/items" with a handler that <acts>
    When a client asks "feed" for "/items"
    Then the client's answer is cut
    And the report holds nothing

    Examples:
      | acts                                    |
      | aborts                                  |
      | flushes the body "partial", then aborts |
      | hijacks the connection, then aborts     |

  Scenario Outline: A route reaches the server's writer through the recover
    Given the plugin "feed" serves "/items" with a handler that <reaches>, then writes the body "done"
    When a client asks "feed" for "/items"
    Then every call the handler made on its writer succeeded
    And the client gets status 200 and "done"

    Examples:
      | reaches                                             |
      | flushes through http.Flusher                        |
      | flushes through a response controller               |
      | sets a read deadline through a response controller  |
      | enables full duplex through a response controller   |

  Scenario Outline: A route that takes over the connection answers on its own
    Given the plugin "feed" serves "/items" with a handler that <takes>, then answers status 200 and "done" on the connection
    When a client asks "feed" for "/items"
    Then every call the handler made on its writer succeeded
    And the client gets status 200 and "done"
    And the report holds nothing

    Examples:
      | takes                                         |
      | hijacks the connection                        |
      | hijacks the connection through http.Hijacker  |

  Scenario: A panic in one plugin's route leaves the other plugins answering
    Given the plugin "feed" serves "/items" with a handler that panics with "boom"
    And the plugin "events" serves "/list" with a handler that answers status 200 and "listed"
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And a client asking "events" for "/list" gets status 200 and "listed"
    And the report holds one failure of "feed" for GET "/items" that the application answered

  Scenario: A plugin that gave no handler gets the application's answer
    Given the plugin "feed" serves no handler
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And the report holds one failure of "feed" for GET "/items" that the application answered

  Scenario: A public route that panics gets the application's answer without the middleware
    Given the plugin "feed" serves the public path "/rss.xml" with a handler that panics with "boom"
    When a client asks "feed" for "/rss.xml"
    Then the client gets status 500 and "internal error"
    And the middleware did not run
    And the report holds one failure of "feed" for GET "/rss.xml" that the application answered

  Scenario: A panic in the application's middleware is never blamed on the plugin
    Given the plugin "feed" serves "/items" with a handler that answers status 200 and "listed"
    And the middleware panics with "session store down"
    When a client asks "feed" for "/items"
    Then the client's answer is cut
    And the report holds nothing

  Scenario: Over HTTP/2, a route that panics before it answers gets the application's answer
    Given the server speaks HTTP/2
    And the plugin "feed" serves "/items" with a handler that panics with "boom"
    When a client asks "feed" for "/items"
    Then the client gets status 500 and "internal error"
    And the answer came over HTTP/2
    And the report holds one failure of "feed" for GET "/items" that the application answered

  Scenario: Over HTTP/2, a route that panics after it flushed is cut
    Given the server speaks HTTP/2
    And the plugin "feed" serves "/items" with a handler that flushes the body "partial", then panics with "boom"
    When a client asks "feed" for "/items"
    Then the client's answer is cut
    And the report holds one failure of "feed" for GET "/items" that the application did not answer

  Scenario Outline: Protect refuses route settings it cannot serve with
    When the application protects a plugin's routes with <missing>
    Then the protect panics with "<text>"

    Examples:
      | missing       | text                                            |
      | no plugin id  | pluginkit: Protect: the plugin id is empty      |
      | no middleware | pluginkit: Protect: RouteSettings.Wrap is nil   |
      | no answer     | pluginkit: Protect: RouteSettings.Answer is nil |
      | no report     | pluginkit: Protect: RouteSettings.Report is nil |

  Scenario: Each plugin's routes and public paths are read on their own
    Given the registered plugins "events", "feed" and "theme"
    And "events" has routes and the public path "/rss.xml"
    And "feed" has routes
    When the application reads the routes of each plugin
    Then the routes hold "events" and "feed"
    And the public paths hold "/rss.xml" for "events" only
    And no plugin failed to load its routes

  Scenario Outline: A plugin whose routes fail to load fails at the step routes
    Given the registered plugins "events" and "feed"
    And "events" has routes and the public path "/rss.xml"
    And "feed" has routes and the public path "/hook"
    And <trouble>
    When the application reads the routes of each plugin
    Then the routes hold "events" only
    And the public paths hold "/rss.xml" for "events" only
    And "feed" failed at the step "routes" with "<text>" <how>

    Examples:
      | trouble                                      | text                                               | how            |
      | the Routes of "feed" panics with "boom"      | pluginkit: feed routes panicked: boom              | as a panic     |
      | the PublicPaths of "feed" panics with "boom" | pluginkit: feed routes panicked: boom              | as a panic     |
      | the Routes of "feed" answers no handler      | pluginkit: feed routes: Routes answered no handler | not as a panic |
