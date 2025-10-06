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
        'table_query'     => true,
    ],

    'priority_tools' => ['json_query', 'text_search', 'file_read', 'git_status', 'git_diff'],

    'categories' => [
        'JSON & Data' => ['json_query', 'table_query'],
        'Text Ops'    => ['text_search', 'text_replace'],
        'Files'       => ['file_read'],
        'Git'         => ['git_status', 'git_diff', 'git_apply_patch'],
        'Help'        => ['help_index', 'help_tool'],
    ],
];
