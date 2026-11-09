# Import a user by its ID, or by its username. Keep `username` in the configuration of a user
# who has one: without it, the next apply deletes the login (the plan warns). Without `password`,
# the login keeps its password; with it, the next apply sets it.
tofu import homeassistant_user.jane 0f8e2c4a6b1d4e3f9a7c5b2d8e6f1a3c
tofu import homeassistant_user.jane jane
