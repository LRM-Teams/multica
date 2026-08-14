import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ApiClient", () => {
  it("loads one Workspace Runner Activity summary projection and fails closed on drift", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [{ agent_id: 42, summary: null }] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.getRunnerActivitySummaries()).resolves.toEqual({ items: [] });
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/members/agents/runner-activity-summaries",
    );
  });

  it("requests presence for the exact session and safely falls back on malformed data", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ session_id: "session-1", presence: null }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchPresence("session-1")).resolves.toEqual({
      session_id: "session-1",
      presence: {},
    });
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/research/sessions/session-1/presence",
    );
  });

  it("uses the canonical encoded Computer delete endpoint and safely degrades malformed data", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: "ok", deleted_count: "two" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.deleteComputer("computer/one")).resolves.toEqual({
      status: "invalid_response",
      daemon_id: "",
      deleted_count: 0,
      deleted_runtime_ids: [],
      tasks_cancelled: 0,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/computers/computer%2Fone",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("keeps a runtime row while degrading an unknown auto-update enum to null", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify([{
        id: "runtime-1",
        workspace_id: "workspace-1",
        daemon_id: "daemon-1",
        name: "Codex",
        runtime_mode: "local",
        provider: "codex",
        launch_header: "",
        status: "online",
        device_info: "",
        metadata: {},
        current_version: "v0.3.72",
        update_state: "idle",
        runtime_health: "ok",
        owner_id: "user-1",
        last_seen_at: "2026-07-27T00:00:00Z",
        created_at: "2026-07-27T00:00:00Z",
        updated_at: "2026-07-27T00:00:00Z",
        auto_update: {
          session_id: "session-1",
          revision: 1,
          observed_at: "2026-07-27T00:00:00Z",
          auto_update_effective_enabled: true,
          config_source: "future_default",
          ineligible_reason: null,
          check_interval_seconds: 21600,
          phase: "waiting",
          attempt_source: null,
          last_attempt_at: null,
          last_outcome: "never_checked",
          target_version: null,
          error_code: null,
          error_message: null,
          staged_version: null,
          activation_generation: null,
          received_at: "2026-07-27T00:00:00Z",
          updated_at: "2026-07-27T00:00:00Z",
        },
      }]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.listRuntimes()).resolves.toMatchObject([
      { id: "runtime-1", auto_update: null },
    ]);
  });

  it("lists explicit Computers independently of Agent runtimes", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            daemon_id: "computer-1",
            owner_id: "user-1",
            connected: true,
            last_seen_at: "2026-08-10T00:00:00Z",
          },
        ]),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.listComputers("workspace-1")).resolves.toEqual([
      expect.objectContaining({ daemon_id: "computer-1", connected: true }),
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/computers?workspace_id=workspace-1",
      expect.anything(),
    );
  });

  it("transcribes PCM through the authenticated voice endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ text: " 你好 " }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const pcm = new ArrayBuffer(4);

    await expect(client.transcribeVoice(pcm)).resolves.toBe("你好");
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/voice/asr",
      expect.objectContaining({
        method: "POST",
        body: pcm,
        headers: expect.objectContaining({ "Content-Type": "audio/pcm; rate=16000" }),
      }),
    );
  });

  it("rejects a non-audio TTS response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response("not audio", { status: 200, headers: { "Content-Type": "text/plain" } }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.synthesizeVoice("hello")).rejects.toMatchObject({ status: 502 });
  });

  it("accepts the self-describing WAV returned by TTS", async () => {
    const wav = new Uint8Array([0x52, 0x49, 0x46, 0x46]).buffer;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(wav, {
        status: 200,
        headers: {
          "Content-Type": "audio/wav",
          "X-Voice-Duration-Ms": "2040",
        },
      }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.synthesizeVoice("hello")).resolves.toEqual({
      audio: wav,
      durationMs: 2040,
    });
  });

  it("keeps TTS audio usable when an older server omits the duration header", async () => {
    const wav = new Uint8Array([0x52, 0x49, 0x46, 0x46]).buffer;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(wav, { status: 200, headers: { "Content-Type": "audio/wav" } }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.synthesizeVoice("hello")).resolves.toEqual({
      audio: wav,
      durationMs: null,
    });
  });

  it("rejects a malformed ASR response instead of reporting no speech", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response("not-json", { status: 200, headers: { "Content-Type": "application/json" } }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.transcribeVoice(new ArrayBuffer(2))).rejects.toMatchObject({ status: 502 });
  });

  it("keeps a valid empty ASR transcript as no speech", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ text: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.transcribeVoice(new ArrayBuffer(2))).resolves.toBe("");
  });

  it("parses the sticker catalog endpoint through the typed client", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        packs: [
          {
            id: "builtin",
            name: "Built-in stickers",
            stickers: [
              {
                pack_id: "builtin",
                sticker_id: "hi",
                asset_url: "/api/stickers/hi",
                alt: "Hi sticker",
                animated: false,
              },
            ],
          },
        ],
      }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await expect(client.listStickers()).resolves.toMatchObject({
      packs: [
        {
          id: "builtin",
          stickers: [
            {
              sticker_id: "hi",
              asset_url: "/api/stickers/hi",
              alt: "Hi sticker",
              tags: [],
            },
          ],
        },
      ],
    });
    expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/stickers", expect.any(Object));
  });

  it("unwraps the project-channels `{ channels, total }` envelope to the list (#629)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        channels: [
          { id: "c1", workspace_id: "ws-1", project_id: "p1", name: "Alpha", description: null, kind: "group", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          { id: "c2", workspace_id: "ws-1", project_id: "p1", name: "Beta", description: null, kind: "group", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
        ],
        total: 2,
      }), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const channels = await client.listProjectChannels("p1", "ws-1");

    // The row iterates the array directly — a leaked envelope object would make
    // `channels.length` undefined and always render the empty state.
    expect(Array.isArray(channels)).toBe(true);
    expect(channels).toHaveLength(2);
    expect(channels[0]).toMatchObject({ id: "c1", name: "Alpha" });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/projects/p1/channels?workspace_id=ws-1",
      expect.any(Object),
    );
  });

  it("preserves HTTP status on failed requests", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "workspace slug already exists" }), {
          status: 409,
          statusText: "Conflict",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");

    try {
      await client.createWorkspace({ name: "Test", slug: "test" });
      throw new Error("expected createWorkspace to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect(error).toMatchObject({
        message: "workspace slug already exists",
        status: 409,
        statusText: "Conflict",
      });
    }
  });

  it("keeps thread-message requests scoped to the thread", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ id: "m-1" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await client.sendChannelThreadMessage("ch-1", "root-1", {
      content: "hello",
      clientMessageId: "client-1",
      quoteMessageId: "quote-1",
    });

    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies).toHaveLength(1);
    expect(bodies[0]).toMatchObject({
      content: "hello",
      client_message_id: "client-1",
      quote_message_id: "quote-1",
    });
    expect(bodies[0]).not.toHaveProperty("show_in_channel");
  });

  it("sends quote_message_id for channel and thread quoted messages", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ id: "m-1" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await client.sendChannelMessage("ch-1", {
      content: "hello",
      clientMessageId: "client-1",
      quoteMessageId: "quote-1",
    });
    await client.sendChannelThreadMessage("ch-1", "root-1", {
      content: "reply",
      clientMessageId: "client-2",
      quoteMessageId: "quote-2",
    });

    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0]).toMatchObject({
      content: "hello",
      client_message_id: "client-1",
      quote_message_id: "quote-1",
    });
    expect(bodies[1]).toMatchObject({
      content: "reply",
      client_message_id: "client-2",
      quote_message_id: "quote-2",
    });
    expect(bodies[0]).not.toHaveProperty("reply_to_message_id");
    expect(bodies[1]).not.toHaveProperty("reply_to_message_id");
  });

  it("sendChannelMessage serialises attachment parts and omits attachment_ids", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ id: "m-1" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const parts = [
      { type: "text" as const, text: "see files" },
      {
        type: "attachment" as const,
        attachment_id: "att-1",
        filename: "a.png",
        content_type: "image/png",
        size_bytes: 12,
      },
      { type: "attachment" as const, attachment_id: "att-2" },
    ];

    await client.sendChannelMessage("ch-1", {
      content: "see files",
      parts,
      clientMessageId: "client-att-1",
    });
    await client.sendChannelThreadMessage("ch-1", "root-1", {
      content: "",
      parts: [{ type: "attachment", attachment_id: "att-only" }],
    });

    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0]).toEqual({
      content: "see files",
      parts,
      client_message_id: "client-att-1",
    });
    expect(bodies[0]).not.toHaveProperty("attachment_ids");
    expect(bodies[1]).toEqual({
      content: "",
      parts: [{ type: "attachment", attachment_id: "att-only" }],
    });
    expect(bodies[1]).not.toHaveProperty("attachment_ids");
  });


  it("emits X-Client-* headers when identity is configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test", {
      identity: { platform: "desktop", version: "1.2.3", os: "macos" },
    });
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBe("desktop");
    expect(headers["X-Client-Version"]).toBe("1.2.3");
    expect(headers["X-Client-OS"]).toBe("macos");
  });

  it("omits X-Client-* headers when identity is not configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBeUndefined();
    expect(headers["X-Client-Version"]).toBeUndefined();
    expect(headers["X-Client-OS"]).toBeUndefined();
  });

  it("sends source_channel_id as a query param on listIssues, and omits it when unset (#476)", async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ issues: [], total: 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listIssues({ status: "todo", source_channel_id: "chan-9" });
    expect(String(fetchMock.mock.calls[0]![0])).toContain("source_channel_id=chan-9");

    fetchMock.mockClear();
    await client.listIssues({ status: "todo" });
    expect(String(fetchMock.mock.calls[0]![0])).not.toContain("source_channel_id");
  });

  it("sends path and include_hidden as query params on listAgentFiles, and omits them when unset", async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ agent_id: "agent-1", status: "ok", nodes: [], truncated: false }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listAgentFiles("agent-1", { include_hidden: true, path: "memory" });
    const listed = String(fetchMock.mock.calls[0]![0]);
    expect(listed).toContain("/api/agents/agent-1/files?");
    expect(listed).toContain("include_hidden=true");
    expect(listed).toContain("path=memory");

    fetchMock.mockClear();
    await client.listAgentFiles("agent-1");
    const root = String(fetchMock.mock.calls[0]![0]);
    expect(root).toContain("/api/agents/agent-1/files");
    expect(root).not.toContain("include_hidden");
    expect(root).not.toContain("path=");
  });

  it("uses the group-local source-issue projection endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ issues: [], total: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listChannelSourceIssues("group-9", { status: "todo", assignee_id: "member-7", limit: 20, offset: 40 });

    const url = String(fetchMock.mock.calls[0]![0]);
    expect(url).toContain("/api/channels/group-9/issues?");
    expect(url).toContain("status=todo");
    expect(url).toContain("assignee_id=member-7");
    expect(url).toContain("limit=20");
    expect(url).toContain("offset=40");
  });

  it("uses the expected HTTP contract for comment trigger preview and suppress", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ agents: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({
          id: "comment-1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "user-1",
          content: "hello",
          type: "comment",
          parent_id: null,
          reactions: [],
          attachments: [],
          created_at: "2026-06-05T00:00:00Z",
          updated_at: "2026-06-05T00:00:00Z",
        }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.previewCommentTriggers("issue-1", "hello", "parent-1");
    await client.createComment(
      "issue-1",
      "hello",
      "comment",
      "parent-1",
      ["attachment-1"],
      ["agent-1"],
    );

    expect(fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method,
      body: init?.body,
    }))).toMatchObject([
      {
        url: "https://api.example.test/api/issues/issue-1/comments/trigger-preview",
        method: "POST",
        body: JSON.stringify({ content: "hello", parent_id: "parent-1" }),
      },
      {
        url: "https://api.example.test/api/issues/issue-1/comments",
        method: "POST",
        body: JSON.stringify({
          content: "hello",
          type: "comment",
          parent_id: "parent-1",
          attachment_ids: ["attachment-1"],
          suppress_agent_ids: ["agent-1"],
        }),
      },
    ]);
  });

  it("uses the Cloud Runtime node API contract", async () => {
    const node = {
      id: "node-1",
      owner_id: "user-1",
      instance_id: "i-0123456789abcdef0",
      region: "us-west-2",
      instance_type: "g5.xlarge",
      image_id: "ami-1",
      subnet_id: "subnet-1",
      name: "gpu-dev-01",
      status: "launching",
      tags: {},
      metadata: {},
      created_at: "2026-05-21T08:30:00Z",
      updated_at: "2026-05-21T08:30:00Z",
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(node), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCloudRuntimeNodes({ limit: 20, offset: 5 });
    await client.createCloudRuntimeNode(
      { instance_type: "g5.xlarge", name: "gpu-dev-01" },
    );

    const listCall = fetchMock.mock.calls[0]!;
    const createCall = fetchMock.mock.calls[1]!;
    expect(listCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes?limit=20&offset=5",
    );
    expect(createCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes",
    );
    expect(createCall[1]).toMatchObject({
      method: "POST",
      body: JSON.stringify({
        instance_type: "g5.xlarge",
        name: "gpu-dev-01",
      }),
    });
  });

  it("falls back when Cloud Runtime node responses drift", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([{ id: 123 }]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: 123 }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await expect(client.listCloudRuntimeNodes()).resolves.toEqual([]);
    await expect(
      client.createCloudRuntimeNode({ instance_type: "g5.xlarge" }),
    ).resolves.toMatchObject({ id: "", status: "" });
  });

  it("deleteCloudRuntimeNode sends DELETE with JSON body containing instance id", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.deleteCloudRuntimeNode("i-0123456789abcdef0");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, opts] = fetchMock.mock.calls[0]!;
    expect(url).toBe("https://api.example.test/api/cloud-runtime/nodes");
    expect(opts).toMatchObject({
      method: "DELETE",
      body: JSON.stringify({ instance_id: "i-0123456789abcdef0" }),
    });
    expect((opts.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
  });

  describe("getAttachment", () => {
    it("returns the parsed attachment for a well-formed response", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              id: "att-1",
              workspace_id: "ws-1",
              issue_id: null,
              comment_id: null,
              uploader_type: "member",
              uploader_id: "u-1",
              filename: "report.md",
              url: "https://static.example.test/ws/att-1.md",
              download_url:
                "https://static.example.test/ws/att-1.md?Policy=p&Signature=s&Key-Pair-Id=k",
              content_type: "text/markdown",
              size_bytes: 123,
              created_at: "2026-05-11T00:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      expect(att.id).toBe("att-1");
      expect(att.download_url).toContain("Policy=");
    });

    it("falls back to an empty attachment when the response is missing download_url", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ id: "att-1" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      // parseWithFallback returns the EMPTY_ATTACHMENT record so callers can
      // safely read `download_url` without crashing — they'll see "" and
      // surface a user-facing error instead of opening `undefined`.
      expect(att.id).toBe("");
      expect(att.download_url).toBe("");
    });
  });

  describe("getAttachmentTextContent", () => {
    it("returns body text and the original content type from the X-* header", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("# heading\n\nbody\n", {
            status: 200,
            headers: {
              "Content-Type": "text/plain; charset=utf-8",
              "X-Original-Content-Type": "text/markdown",
            },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const { text, originalContentType } =
        await client.getAttachmentTextContent("att-1");

      expect(text).toBe("# heading\n\nbody\n");
      expect(originalContentType).toBe("text/markdown");
    });

    it("throws PreviewTooLargeError on 413", async () => {
      const { PreviewTooLargeError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 413, statusText: "Payload Too Large" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewTooLargeError,
      );
    });

    it("throws PreviewUnsupportedError on 415", async () => {
      const { PreviewUnsupportedError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 415, statusText: "Unsupported Media Type" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewUnsupportedError,
      );
    });
  });

  describe("listChatMessagesPage deployment-order fallback", () => {
    const jsonResponse = (body: unknown, status: number, statusText = "") =>
      new Response(JSON.stringify(body), {
        status,
        statusText,
        headers: { "Content-Type": "application/json" },
      });

    it("falls back to the legacy full-list endpoint when the paged route 404s", async () => {
      const legacy = [
        { id: "m1", role: "user", content: "hi", created_at: "2026-06-01T00:00:00Z" },
        { id: "m2", role: "assistant", content: "yo", created_at: "2026-06-01T00:00:01Z" },
      ];
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ error: "not found" }, 404, "Not Found"))
        .mockResolvedValueOnce(jsonResponse(legacy, 200));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChatMessagesPage("session-1", { limit: 50 });

      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages/page?limit=50",
      );
      expect(fetchMock.mock.calls[1]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages",
      );
      expect(page).toEqual({ messages: legacy, limit: 50, has_more: false, next_cursor: null });
    });

    it("does NOT fall back on a cursor request — a 404 there propagates", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "not found" }, 404, "Not Found"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(
        client.listChatMessagesPage("session-1", {
          before: { created_at: "2026-06-01T00:00:00Z", id: "m1" },
        }),
      ).rejects.toBeInstanceOf(ApiError);
      // Only the paged request fires; no legacy full-list call that would duplicate messages.
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it("propagates non-404 errors instead of masking them with the legacy list", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "boom" }, 500, "Internal Server Error"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(client.listChatMessagesPage("session-1")).rejects.toMatchObject({
        status: 500,
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });

  describe("channel message sequence pagination", () => {
    const channelMessage = {
      id: "msg-1",
      channel_id: "channel-1",
      workspace_id: "ws-1",
      seq: 42,
      type: "user",
      author_id: "user-1",
      author_name: "User",
      content: "hello",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: "2026-07-03T00:00:00Z",
    };

    it("uses before_seq for channel message page cursors", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          messages: [channelMessage],
          limit: 50,
          has_more: true,
          next_cursor: { seq: 42, created_at: channelMessage.created_at, id: channelMessage.id },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChannelMessagesPage("channel-1", {
        before: { seq: 42, created_at: channelMessage.created_at, id: channelMessage.id },
      });

      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/channels/channel-1/messages?limit=50&before_seq=42",
      );
      expect(page.next_cursor).toEqual({ seq: 42, created_at: channelMessage.created_at, id: channelMessage.id });
    });

    it("uses around_seq (task #340) and parses the bidirectional page fields", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          messages: [channelMessage],
          limit: 50,
          has_more: true,
          next_cursor: { seq: 30, created_at: channelMessage.created_at, id: "older-1" },
          anchor_index: 3,
          has_more_after: true,
          after_cursor: { seq: 60, created_at: channelMessage.created_at, id: "newer-1" },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChannelMessagesPage("channel-1", { around: 42 });

      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/channels/channel-1/messages?limit=50&around_seq=42",
      );
      expect(page.anchor_index).toBe(3);
      expect(page.has_more_after).toBe(true);
      expect(page.after_cursor).toEqual({ seq: 60, created_at: channelMessage.created_at, id: "newer-1" });
      // Older-side cursor still carried in around mode.
      expect(page.next_cursor).toEqual({ seq: 30, created_at: channelMessage.created_at, id: "older-1" });
    });

    it("prefers around_seq over before when both are supplied (server rejects both)", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ messages: [], limit: 50, has_more: false }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.listChannelMessagesPage("channel-1", {
        around: 42,
        before: { seq: 99, created_at: channelMessage.created_at, id: channelMessage.id },
      });

      const url = fetchMock.mock.calls[0]![0] as string;
      expect(url).toContain("around_seq=42");
      expect(url).not.toContain("before_seq");
    });

    it("uses and returns before_seq for thread cursors", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          messages: [channelMessage],
          next_cursor: {
            before_seq: 42,
            before: channelMessage.created_at,
            before_id: channelMessage.id,
          },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChannelMessageThread("channel-1", "root-1", {
        beforeSeq: 42,
        before: channelMessage.created_at,
        beforeId: channelMessage.id,
      });

      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/channels/channel-1/messages/root-1/thread?before_seq=42",
      );
      expect(page.next_cursor).toEqual({
        before_seq: 42,
        before: channelMessage.created_at,
        before_id: channelMessage.id,
      });
    });

    it("falls back when channel messages page response is malformed", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ messages: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChannelMessagesPage("channel-1", { limit: 25 });

      expect(page).toEqual({ messages: [], limit: 25, has_more: false, next_cursor: null });
    });

    it("falls back when channel thread response is malformed", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ messages: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChannelMessageThread("channel-1", "root-1");

      expect(page).toEqual({ messages: [], next_cursor: null });
    });
  });

  describe("cancelTaskById response parsing", () => {
    const taskResponse = {
      id: "task-1",
      agent_id: "agent-1",
      runtime_id: "runtime-1",
      issue_id: "",
      status: "cancelled",
      priority: 0,
      dispatched_at: null,
      started_at: null,
      completed_at: "2026-06-12T06:40:00Z",
      result: null,
      error: null,
      created_at: "2026-06-12T06:39:00Z",
    };

    it("parses the cancelled chat message payload", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: true,
          },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(fetchMock.mock.calls[0]).toMatchObject([
        "https://api.example.test/api/tasks/task-1/cancel",
        { method: "POST" },
      ]);
      expect(result.cancelled_chat_message).toEqual({
        chat_session_id: "session-1",
        message_id: "message-1",
        content: "restore me",
        restore_to_input: true,
      });
    });

    it("treats a null cancelled chat message as absent", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({
            ...taskResponse,
            cancelled_chat_message: null,
          }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("task-1");
      expect(result.cancelled_chat_message).toBeUndefined();
    });

    it.each([
      ["a missing task id", { ...taskResponse, id: undefined }],
      [
        "a malformed cancelled chat message",
        {
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: "true",
          },
        },
      ],
      ["a null body", null],
    ])("falls back for %s", async (_label, body) => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("");
      expect(result.cancelled_chat_message).toBeUndefined();
    });
  });

  describe("chat attachment wiring", () => {
    const uploadOkBody = {
      id: "att-1",
      url: "https://cdn/x",
      download_url: "/api/attachments/att-1/download",
      markdown_url: "https://cdn/x.md",
      filename: "hi.png",
      content_type: "image/png",
      size_bytes: 2,
      created_at: "2026-01-01T00:00:00Z",
      workspace_id: "ws",
      uploader_type: "member",
      uploader_id: "u1",
    };

    it("uploadFile includes chat_session_id in the FormData body", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify(uploadOkBody), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      const att = await client.uploadFile(file, { chatSessionId: "session-123" });

      expect(att.id).toBe("att-1");
      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0]!;
      expect(url).toBe("https://api.example.test/api/upload-file");
      expect(init?.method).toBe("POST");
      const body = init?.body as FormData;
      expect(body).toBeInstanceOf(FormData);
      expect(body.get("chat_session_id")).toBe("session-123");
      expect(body.get("issue_id")).toBeNull();
      expect(body.get("comment_id")).toBeNull();
    });

    it("uploadFile includes channel_id in the FormData body", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ ...uploadOkBody, channel_id: "ch-1" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await client.uploadFile(file, { channelId: "ch-1" });

      const body = fetchMock.mock.calls[0]![1]?.body as FormData;
      expect(body.get("channel_id")).toBe("ch-1");
    });

    it("uploadFile throws on schema mismatch instead of EMPTY_ATTACHMENT (LRM-426)", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ id: "att-1", url: "https://cdn/x" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await expect(client.uploadFile(file)).rejects.toThrow(/Upload response invalid/);
    });

    it("uploadFile throws when the API returns an empty attachment id", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              ...uploadOkBody,
              id: "",
            }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await expect(client.uploadFile(file)).rejects.toThrow(/missing attachment id/);
    });

    it("uploadFile surfaces the API error body message on non-2xx", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ error: "not a channel member" }), {
            status: 403,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await expect(client.uploadFile(file, { channelId: "ch-1" })).rejects.toThrow(
        "not a channel member",
      );
    });

    it("sendChatMessage serialises attachment_ids onto the JSON body when present", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello", ["att-1", "att-2"]);

      const [, init] = fetchMock.mock.calls[0]!;
      expect(JSON.parse(init?.body as string)).toEqual({
        content: "hello",
        attachment_ids: ["att-1", "att-2"],
      });
    });

    it("sendChatMessage omits attachment_ids when the list is empty or undefined", async () => {
      const fetchMock = vi.fn().mockImplementation(() =>
        Promise.resolve(
          new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello");
      await client.sendChatMessage("session-1", "again", []);

      expect(JSON.parse(fetchMock.mock.calls[0]![1]?.body as string)).toEqual({ content: "hello" });
      expect(JSON.parse(fetchMock.mock.calls[1]![1]?.body as string)).toEqual({ content: "again" });
    });
  });

  it("reads and updates the memory curator profile through workspace routes", async () => {
    const profile = {
      id: "profile-1",
      workspace_id: "ws-1",
      user_id: "user-1",
      runtime_id: "runtime-1",
      curator_agent_id: "agent-1",
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(profile), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(profile), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await client.getMemoryCuratorProfile("ws-1");
    await client.updateMemoryCuratorProfile("ws-1", {
      enabled: true,
      self_review_enabled: true,
      team_curation_enabled: true,
      mode: "review",
      runtime_id: "runtime-1",
      curator_agent_id: "agent-1",
      target_scope: "owned_all",
      target_agent_ids: [],
      timezone: "Asia/Shanghai",
      schedule_hour: 1,
      catch_up_enabled: true,
      confidence_threshold: 0.8,
    });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/workspaces/ws-1/memory-curation/profile");
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({ method: "PUT" });
  });

  it("queues a staged memory curation run with dry-run preserved", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: "run-1", status: "queued" }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await client.startMemoryCurationRun("ws-1", { all_agents: true, stage: "team_curation", dry_run: true });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/workspaces/ws-1/memory-curation/runs");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      all_agents: true,
      stage: "team_curation",
      dry_run: true,
    });
  });

  it("starts memory curation backfill for a date range", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        since: "2026-06-22",
        until: "2026-07-21",
        dry_run: false,
        queued: [{ date: "2026-07-18", stage: "all", target_agent_ids: ["a1"], run_id: "run-1", status: "queued" }],
        skipped: [{ date: "2026-07-17", reason: "no_activity" }],
        queued_days: 1,
        skip_days: 1,
      }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const result = await client.startMemoryCurationBackfill("ws-1", {
      since: "2026-06-22",
      until: "2026-07-21",
    });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/workspaces/ws-1/memory-curation/backfill");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      since: "2026-06-22",
      until: "2026-07-21",
    });
    expect(result.queued_days).toBe(1);
    expect(result.queued[0]?.date).toBe("2026-07-18");
  });

  describe("getAgentReminders response parsing", () => {
    it("falls back to an empty page when definitions/occurrences arrive as null", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ definitions: null, occurrences: null, limit: 20, has_more: false }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.getAgentReminders("agent-1", { status: "scheduled" });

      // definitions/occurrences arriving as null (not a missing key) fails
      // the array schema outright, so the whole page falls back to the safe
      // empty constant — including limit — rather than partially trusting
      // the rest of a response that already violated the contract.
      expect(page).toEqual({ definitions: [], occurrences: [], limit: 0, has_more: false });
    });

    it("falls back to an empty page when a field is the wrong type entirely", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ definitions: "not-an-array", occurrences: [], limit: 20, has_more: false }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.getAgentReminders("agent-1", { status: "scheduled" });

      expect(page).toEqual({ definitions: [], occurrences: [], limit: 0, has_more: false });
    });

    it("keeps a well-formed page intact, including an unrecognized schedule_kind on one row (row-level narrowing happens in the adapter, not here)", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            definitions: [
              {
                id: "r-1",
                title: "Ping standup",
                status: "scheduled",
                schedule_kind: "recurring",
                next_fire_at: "2026-07-24T09:00:00Z",
                cadence: "daily@09:00",
                schedule_timezone: "America/Los_Angeles",
                snooze_count: 0,
                anchor: { available: false },
              },
              {
                id: "r-2",
                title: "Unknown future kind",
                status: "scheduled",
                schedule_kind: "some_future_kind",
                next_fire_at: "2026-07-25T09:00:00Z",
                snooze_count: 0,
                anchor: { available: false },
              },
            ],
            occurrences: [],
            limit: 20,
            has_more: false,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.getAgentReminders("agent-1", { status: "scheduled" });

      // The schema layer is intentionally lenient — it parses both rows
      // (an unknown schedule_kind doesn't reject the whole page). Dropping
      // the row with the unrecognized kind is adaptUpcomingRow's job.
      expect(page.definitions).toHaveLength(2);
      expect(page.definitions[1]?.schedule_kind).toBe("some_future_kind");
    });

  });

  describe("agent lifecycle (#633)", () => {
    it("starts a lifecycle action with the Idempotency-Key header and action_kind only", async () => {
      const op = {
        id: "op-1",
        agent_id: "a-1",
        runtime_id: "rt-1",
        action_kind: "full_reset_restart",
        status: "scheduled",
        execution_mode: "after_current_run",
        created_at: "2026-07-24T00:00:00Z",
      };
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify(op), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);
      const client = new ApiClient("https://api.example.test");

      const result = await client.startAgentLifecycleAction(
        "a-1",
        "full_reset_restart",
        "idem-uuid-1",
      );

      expect(result.id).toBe("op-1");
      expect(fetchMock).toHaveBeenCalledWith(
        "https://api.example.test/api/members/agents/a-1/lifecycle",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ action_kind: "full_reset_restart" }),
          headers: expect.objectContaining({ "Idempotency-Key": "idem-uuid-1" }),
        }),
      );
    });

    it("reads the per-action preflight", async () => {
      const preflight = {
        actions: {
          restart: { supported: true, execution_mode: "immediate" },
          reset_session_restart: { supported: true, execution_mode: "immediate" },
          full_reset_restart: {
            supported: false,
            disabled_reason: "agent_active",
            execution_mode: "immediate",
          },
        },
      };
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify(preflight), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
      const client = new ApiClient("https://api.example.test");

      const result = await client.getAgentLifecyclePreflight("a-1");
      expect(result.actions.restart.supported).toBe(true);
      expect(result.actions.full_reset_restart.supported).toBe(false);
      expect(result.actions.full_reset_restart.disabled_reason).toBe("agent_active");
    });

    it("polls a single operation by id", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            id: "op-1",
            agent_id: "a-1",
            runtime_id: "rt-1",
            action_kind: "restart",
            status: "succeeded",
            execution_mode: "immediate",
            created_at: "2026-07-24T00:00:00Z",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
      vi.stubGlobal("fetch", fetchMock);
      const client = new ApiClient("https://api.example.test");

      const op = await client.getAgentLifecycleOperation("a-1", "op-1");
      expect(op.status).toBe("succeeded");
      expect(fetchMock.mock.calls[0]?.[0]).toBe(
        "https://api.example.test/api/members/agents/a-1/lifecycle/op-1",
      );
    });
  });
});
