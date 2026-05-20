"""Package steps — tutti i passi di installazione DoorPhoneServer."""

from steps.system_check import StepSystemCheck
from steps.hostname     import StepHostname
from steps.create_user  import StepCreateUser
from steps.packages     import StepPackages
from steps.golang       import StepGolang
from steps.audio        import StepAudioConfig
from steps.mumble       import StepMumbleServer
from steps.boot_config  import StepBootConfig
from steps.clone_build  import StepCloneAndBuild
from steps.data_dir     import StepDataDir
from steps.systemd      import StepSystemdService
from steps.log2ram      import StepLog2Ram
from steps.code_server  import StepCodeServer
from steps.env_config   import StepEnvConfig
from steps.cleanup      import StepCleanup

from typing import List
from lib.step_base import Step


def build_steps() -> List[Step]:
    return [
        StepSystemCheck(),
        StepEnvConfig(),
        StepHostname(),
        StepCreateUser(),
        StepPackages(),
        StepGolang(),
        StepAudioConfig(),
        StepMumbleServer(),
        StepBootConfig(),
        StepCloneAndBuild(),
        StepDataDir(),
        StepSystemdService(),
        StepLog2Ram(),
        StepCodeServer(),
        StepCleanup(),
    ]
