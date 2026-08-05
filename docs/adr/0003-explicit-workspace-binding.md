# Require explicit setup for each Workspace Execution Binding

The Machine Service discovers every Workspace membership available to the
signed-in user, but discovery never creates a local execution binding. The user
must explicitly run `multica setup /<workspace>` on each machine that should
execute Agents for that Workspace. This keeps membership visibility automatic
while making credential installation, resource consumption, and local execution
an intentional per-machine choice.
