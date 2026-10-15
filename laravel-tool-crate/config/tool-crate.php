<?php

return [
    'enabled_tools' => [
        'json_query'      => true,
        'text_search'     => true,
        'file_read'       => true,
        'text_replace'    => true,
        'help_index'      => true,
        'help_tool'       => true,
        'git_status'      => true,
        'git_diff'        => true,
        'git_apply_patch' => true,
        'git_sandbox'     => true,
        'table_query'     => true,
        'db_query'        => true,
        'db_inspect'      => true,
        'db_schema_dump'  => true,
    ],

    'priority_tools' => ['json_query', 'text_search', 'file_read', 'git_status', 'git_diff', 'db_query'],

    'categories' => [
        'JSON & Data' => ['json_query', 'table_query'],
        'Text Ops'    => ['text_search', 'text_replace'],
        'Files'       => ['file_read'],
        'Git'         => ['git_status', 'git_diff', 'git_apply_patch', 'git_sandbox'],
        'Database'    => ['db_query', 'db_inspect', 'db_schema_dump'],
        'Help'        => ['help_index', 'help_tool'],
    ],
];
